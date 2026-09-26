package kafka

import "testing"

// BenchmarkProducerConfig 基准生产者配置构建(解析 broker 列表 + 组装 Writer, 不含网络连接).
func BenchmarkProducerConfig(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewProducer("kafka1:9092,kafka2:9092,kafka3:9092")
	}
}

// BenchmarkConsumerConfig 基准消费者配置构建(解析 broker 列表 + 组装 Reader, 不含网络连接).
func BenchmarkConsumerConfig(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewConsumer("kafka1:9092,kafka2:9092", "device-telemetry", "onepark-group")
	}
}
