from openai import OpenAI

# Create OpenAI client
client = OpenAI(
    api_key="your_api_key_here"
)

# Send request to AI model
response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[
        {
            "role": "system",
            "content": "You are an expert Kubernetes SRE assistant."
        },
        {
            "role": "user",
            "content": """
A Kubernetes pod restarted frequently.

Alert Details:
- Pod: payment-service
- Namespace: superheroes
- Severity: warning

Please explain:
1. Possible root causes
2. Troubleshooting steps
3. Recommended fixes
"""
        }
    ]
)

# Print response
print("\n===== AI RESPONSE =====\n")
print(response.choices[0].message.content)