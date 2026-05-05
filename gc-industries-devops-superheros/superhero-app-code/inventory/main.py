from fastapi import FastAPI

app = FastAPI()

inventory_db = {
    1: {"name": "War Machine Mark I", "stock": 5},
    2: {"name": "War Machine Mark II", "stock": 3}
}

@app.get("/health")
def health():
    return {"service": "inventory", "status": "ok"}

@app.get("/inventory")
def inventory():
    return inventory_db

@app.get("/inventory/{item_id}")
def item(item_id: int):
    return inventory_db.get(item_id, {"error": "not found"})

# test comment for pipeline
# another test comment for pipeline