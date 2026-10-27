package services

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 扇出队列、重试队列与死信队列。写扩散是唯一需要异步化的链路：
// 一次发布要写到 N 个粉丝的时间线，N 随粉丝数增长，必须移出主请求。
const (
	fanoutQueue      = "feed.fanout"
	fanoutRetryQueue = "feed.fanout.retry"
	fanoutDLQ        = "feed.fanout.dlq"

	// 扇出失败后最多重试三次，每次间隔由重试队列的 TTL 控制
	fanoutRetryLimit   = 3
	fanoutRetryDelayMS = 5000
	fanoutRetryHeader  = "x-retry"
)

// fanoutTask 一条扇出任务：把某个视频写进作者的粉丝时间线
type fanoutTask struct {
	VideoID  uint `json:"video_id"`
	AuthorID uint `json:"author_id"`
}

// mqAddr 读取 MQ 地址，未设置时用本地默认值
func mqAddr() string {
	if v := os.Getenv("FEED_MQ_ADDR"); v != "" {
		return v
	}
	return "amqp://guest:guest@127.0.0.1:5672/"
}

var (
	mqConn  *amqp.Connection
	mqChan  *amqp.Channel
	mqReady bool
)

// declareFanoutQueues 声明业务队列、重试队列与死信队列。
// 重试队列不设消费者：消息在队列里停留到 TTL 到期，按死信路由回到业务队列，形成延迟重试。
func declareFanoutQueues(ch *amqp.Channel) error {
	if _, err := ch.QueueDeclare(fanoutQueue, true, false, false, false, amqp.Table{
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": fanoutDLQ,
	}); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(fanoutRetryQueue, true, false, false, false, amqp.Table{
		"x-message-ttl":             int32(fanoutRetryDelayMS),
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": fanoutQueue,
	}); err != nil {
		return err
	}
	_, err := ch.QueueDeclare(fanoutDLQ, true, false, false, false, nil)
	return err
}

// SetupFanoutPublisher 建立投递侧连接，供 API 进程使用；失败时调用方退回同步扇出
func SetupFanoutPublisher() error {
	if os.Getenv("FEED_MQ_DISABLED") == "1" {
		return errors.New("消息队列已关闭")
	}

	conn, err := amqp.Dial(mqAddr())
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return err
	}
	if err := declareFanoutQueues(ch); err != nil {
		conn.Close()
		return err
	}

	mqConn, mqChan, mqReady = conn, ch, true
	return nil
}

// publishFanout 投递一条扇出任务；未就绪或投递失败时返回错误，调用方退回同步扇出
func publishFanout(authorID, videoID uint) error {
	if !mqReady {
		return errors.New("消息队列未就绪")
	}
	body, err := json.Marshal(fanoutTask{VideoID: videoID, AuthorID: authorID})
	if err != nil {
		return err
	}
	return mqChan.Publish("", fanoutQueue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Body:         body,
	})
}

// ConsumeFanout 以阻塞方式消费扇出任务，供 Worker 进程使用。
// 连接断开时返回，由调用方重连；消息格式错误直接进死信，业务处理本身可重入。
func ConsumeFanout() error {
	conn, err := amqp.Dial(mqAddr())
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if err := declareFanoutQueues(ch); err != nil {
		return err
	}

	deliveries, err := ch.Consume(fanoutQueue, "feed-fanout-worker", false, false, false, false, nil)
	if err != nil {
		return err
	}

	for d := range deliveries {
		var task fanoutTask
		if err := json.Unmarshal(d.Body, &task); err != nil {
			log.Println("扇出任务格式错误，转入死信:", err)
			_ = d.Nack(false, false)
			continue
		}
		if err := fanoutVideo(task.AuthorID, task.VideoID); err != nil {
			if ok, rerr := retryFanout(ch, d, err); !ok {
				log.Println("扇出重试处理失败，转入死信:", rerr)
				_ = d.Nack(false, false)
			}
			continue
		}
		_ = d.Ack(false)
	}
	return nil
}

// fanoutRetryCount 读出这条消息已经重试过几次
func fanoutRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	switch v := headers[fanoutRetryHeader].(type) {
	case int32:
		return int(v)
	case int64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// fanoutRetryPlan 判断还能不能重试：返回已重试次数，以及是否继续重试
func fanoutRetryPlan(headers amqp.Table) (attempt int, retry bool) {
	attempt = fanoutRetryCount(headers)
	return attempt, attempt < fanoutRetryLimit
}

// retryFanout 处理失败的消息：还能重试就投进重试队列，超过上限就交给队列的死信路由。
// 原消息的确认也在这里完成，返回 ok=false 表示这条消息还没被确认，由调用方兜底。
func retryFanout(ch *amqp.Channel, d amqp.Delivery, cause error) (ok bool, err error) {
	attempt, retry := fanoutRetryPlan(d.Headers)
	if !retry {
		log.Printf("扇出重试 %d 次仍失败，转入死信: %v", attempt, cause)
		if err := d.Nack(false, false); err != nil {
			return false, err
		}
		return true, nil
	}
	log.Printf("扇出失败，%d 毫秒后第 %d 次重试: %v", fanoutRetryDelayMS, attempt+1, cause)
	if err := ch.Publish("", fanoutRetryQueue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
		Headers:      amqp.Table{fanoutRetryHeader: int32(attempt + 1)},
		Body:         d.Body,
	}); err != nil {
		return false, err
	}
	if err := d.Ack(false); err != nil {
		return false, err
	}
	return true, nil
}
