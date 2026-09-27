package svc

import (
	"testing"

	"onepark/app/alarm-service/internal/config"
	"onepark/app/alarm-service/internal/notify"
)

// TestNewNotifier_DisabledWithoutBrokers 未配置 Kafka broker 时通知器为 nil(通知跳过), 不报错.
func TestNewNotifier_DisabledWithoutBrokers(t *testing.T) {
	cases := map[string]string{
		"空配置":    "",
		"仅空白":    "   ",
		"未展开占位符": "${KAFKA_BROKERS}", // go-zero 在变量缺失时保留字面量
	}
	for name, brokers := range cases {
		c := config.Config{Kafka: config.KafkaConf{Brokers: brokers}}
		if n := newNotifier(c); n != nil {
			t.Errorf("%s: 应视为未配置 Kafka 并返回 nil, 实际 %#v", name, n)
		}
	}
}

// TestNewNotifier_TopicFallback topic 缺省与未展开占位符都应回落到默认 topic.
func TestNewNotifier_TopicFallback(t *testing.T) {
	cases := map[string]string{
		"未配置 topic": "",
		"仅空白":       "   ",
		"占位符未展开":    "${ALARM_NOTIFY_TOPIC}",
	}
	for name, topic := range cases {
		c := config.Config{
			Kafka:  config.KafkaConf{Brokers: "127.0.0.1:9092"},
			Notify: config.NotifyConf{Topic: topic},
		}
		n := newNotifier(c)
		kn, ok := n.(*notify.KafkaNotifier)
		if !ok {
			t.Fatalf("%s: 应装配 KafkaNotifier, 实际 %T", name, n)
		}
		if kn.Topic() != notify.DefaultTopic {
			t.Errorf("%s: topic 应回落默认值 %s, 实际 %s", name, notify.DefaultTopic, kn.Topic())
		}
	}
}

// TestNewNotifier_UsesConfiguredTopic 配置了 topic 时按配置装配, 便于联调期改名零改代码.
func TestNewNotifier_UsesConfiguredTopic(t *testing.T) {
	c := config.Config{
		Kafka:  config.KafkaConf{Brokers: "broker-a:9092,broker-b:9092"},
		Notify: config.NotifyConf{Topic: " onepark.alarm.event.dev "},
	}
	n := newNotifier(c)
	kn, ok := n.(*notify.KafkaNotifier)
	if !ok {
		t.Fatalf("应装配 KafkaNotifier, 实际 %T", n)
	}
	if kn.Topic() != "onepark.alarm.event.dev" {
		t.Errorf("topic 应去除首尾空白后使用, 实际 %q", kn.Topic())
	}
}
