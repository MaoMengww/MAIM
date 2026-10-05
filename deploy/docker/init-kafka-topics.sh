#!/bin/sh
set -eu

for topic in message.created message.recalled message.edited message.deleted \
    conversation.read.updated conversation.bot.added delivery.requested; do
    /opt/kafka/bin/kafka-topics.sh --bootstrap-server kafka:9092 \
        --create --if-not-exists --topic "$topic" --partitions 3 --replication-factor 1
done
