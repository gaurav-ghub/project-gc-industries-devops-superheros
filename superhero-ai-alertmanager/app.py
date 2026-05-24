from fastapi import FastAPI
from openai import OpenAI
from dotenv import load_dotenv
import requests
import os

# Load environment variables
load_dotenv()

app = FastAPI()

# Read environment variables
OPENAI_API_KEY = os.getenv("OPENAI_API_KEY")
SLACK_WEBHOOK_URL = os.getenv("SLACK_WEBHOOK_URL")

# OpenAI client
client = OpenAI(
    api_key=OPENAI_API_KEY
)

# Function to send messages to Slack
def send_to_slack(message):

    slack_data = {
        "text": message
    }

    response = requests.post(
        SLACK_WEBHOOK_URL,
        json=slack_data
    )

    print("\n===== SLACK RESPONSE =====")
    print(response.status_code)


@app.get("/")
def home():
    return {
        "message": "Superhero AI Alertmanager is running"
    }


@app.post("/alerts")
async def receive_alert(alert: dict):

    print("\n===== ALERT RECEIVED =====\n")
    print(alert)

    response = client.chat.completions.create(
        model="gpt-4o-mini",
        messages=[
            {
                "role": "system",
                "content": """
You are a senior Kubernetes Site Reliability Engineer.

Analyze Kubernetes alerts professionally.

Provide response in this exact format:

🚨 Issue Summary
- short explanation

🔍 Probable Root Cause
- concise root cause

🛠 Recommended Actions
- 3 practical troubleshooting steps

⚡ Suggested Fix
- short remediation advice

Keep response concise, practical, and production-focused.
Maximum 150 words.
"""
            },
            {
                "role": "user",
                "content": f"""
Analyze this Kubernetes alert:

{alert}
"""
            }
        ]
    )

    ai_response = response.choices[0].message.content

    print("\n===== AI RESPONSE =====\n")
    print(ai_response)

    # Send AI response to Slack
    send_to_slack(ai_response)

    return {
        "status": "success",
        "ai_analysis": ai_response
    }