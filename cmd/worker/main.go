package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"feed/bootstrap"
	"feed/services"
)

// 写扩散消费者：只连数据库、Redis 和消息队列，不对外提供 HTTP。
// 连接断开后退避重连，API 不受影响；收到退出信号时停止拉取新消息，
// 手上那条处理完再退出，未确认的消息会由队列重新投递。
func main() {
	bootstrap.OpenDB()
	bootstrap.OpenRedis()

	// 补投需要一条投递连接，连不上时先跳过，消费照常进行
	if err := services.SetupFanoutPublisher(); err != nil {
		log.Println("扇出补投暂时不可用:", err)
	}
	go services.StartFanoutSweeper()

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		log.Println("收到退出信号，停止拉取新消息，等待当前消息处理完")
		close(stop)
	}()

	log.Println("写扩散消费者启动")

	for {
		select {
		case <-stop:
			log.Println("消费者已停止")
			return
		default:
		}

		if err := services.ConsumeFanout(stop); err != nil {
			select {
			case <-stop:
				log.Println("消费者已停止")
				return
			default:
			}
			log.Println("消费者中断，5 秒后重连:", err)
		} else {
			select {
			case <-stop:
				log.Println("消费者已停止")
				return
			default:
			}
			log.Println("消费者连接已关闭，5 秒后重连")
		}

		select {
		case <-stop:
			log.Println("消费者已停止")
			return
		case <-time.After(5 * time.Second):
		}
	}
}
