from kafka import KafkaProducer
import json
import time

producer = KafkaProducer(
    bootstrap_servers='localhost:9092',
    value_serializer=lambda v: json.dumps(v).encode('utf-8')
)

heroes = [
    {"hero": "Batman", "power": "Money"},
    {"hero": "Superman", "power": "Strength"},
    {"hero": "Flash", "power": "Speed"}
]

for hero in heroes:
    producer.send('superheroes', hero)
    print(f"Sent: {hero}")
    time.sleep(2)

producer.flush()