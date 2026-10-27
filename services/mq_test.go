package services

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestFanoutRetryPlan(t *testing.T) {
	cases := []struct {
		name        string
		headers     amqp.Table
		wantAttempt int
		wantRetry   bool
	}{
		{"首次失败", nil, 0, true},
		{"已重试一次", amqp.Table{fanoutRetryHeader: int32(1)}, 1, true},
		{"已重试两次", amqp.Table{fanoutRetryHeader: int32(2)}, 2, true},
		{"达到上限", amqp.Table{fanoutRetryHeader: int32(3)}, 3, false},
		{"超过上限", amqp.Table{fanoutRetryHeader: int32(5)}, 5, false},
		{"头部类型不符", amqp.Table{fanoutRetryHeader: "x"}, 0, true},
	}
	for _, c := range cases {
		attempt, retry := fanoutRetryPlan(c.headers)
		if attempt != c.wantAttempt || retry != c.wantRetry {
			t.Errorf("%s: 得到 (%d, %v)，期望 (%d, %v)", c.name, attempt, retry, c.wantAttempt, c.wantRetry)
		}
	}
}
