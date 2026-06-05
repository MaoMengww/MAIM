package kafka

import (
	"strconv"
	"time"

	"github.com/IBM/sarama"
	"github.com/maomeng/aim/pkg/metrics"
)

var (
	KafkaConsumerLag = metrics.NewGaugeVec("kafka_consumer_lag",
		"Kafka consumer lag per partition", "topic", "partition", "group")
)

func CollectConsumerLag(client sarama.Client, groupID string, topics []string) {
	go func() {
		for {
			for _, topic := range topics {
				partitions, err := client.Partitions(topic)
				if err != nil {
					continue
				}
				for _, partition := range partitions {
					newest, err := client.GetOffset(topic, partition, sarama.OffsetNewest)
					if err != nil {
						continue
					}
					coord, err := client.Coordinator(groupID)
					if err != nil {
						continue
					}
					req := &sarama.OffsetFetchRequest{
						Version:       1,
						ConsumerGroup: groupID,
					}
					req.AddPartition(topic, partition)
					resp, err := coord.FetchOffset(req)
					if err != nil {
						continue
					}
					block := resp.GetBlock(topic, partition)
					if block == nil || block.Err != sarama.ErrNoError {
						continue
					}
					committed := block.Offset
					lag := newest - committed
					if lag < 0 {
						lag = 0
					}
					KafkaConsumerLag.Set(float64(lag), topic, strconv.Itoa(int(partition)), groupID)
				}
			}
			time.Sleep(15 * time.Second)
		}
	}()
}
