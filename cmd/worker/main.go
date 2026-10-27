package main

import (
	"log"
	"time"

	"feed/bootstrap"
	"feed/services"
)

// 写扩散消费者：只连数据库、Redis 和消息队列，不对外提供 HTTP。
// 连接断开后退避重连，API 不受影响。
func main() {
	bootstrap.OpenDB()
	bootstrap.OpenRedis()

	// 补投需要一条投递连接，连不上时先跳过，消费照常进行
	if err := services.SetupFanoutPublisher(); err != nil {
		log.Println("扇出补投暂时不可用:", err)
	}
	go services.StartFanoutSweeper()

	log.Println("写扩散消费者启动")

	for {
		if err := services.ConsumeFanout(); err != nil {
			log.Println("消费者中断，5 秒后重连:", err)
		} else {
			log.Println("消费者连接已关闭，5 秒后重连")
		}
		time.Sleep(5 * time.Second)
	}
}
