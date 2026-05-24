from kafka import KafkaConsumer
import json

consumer = KafkaConsumer(
    'superheroes',
    bootstrap_servers='localhost:9092',
    auto_offset_reset='earliest',
    group_id='hero-group',
    value_deserializer=lambda x: json.loads(x.decode('utf-8'))
)

print("Waiting for messages...")

for message in consumer:
    print(f"Received: {message.value}")