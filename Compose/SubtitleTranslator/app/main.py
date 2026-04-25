import os
import sys
from fastapi import FastAPI

APP_PORT = os.environ.get("APP_PORT", "9800")
LLM_API_URL = os.environ.get("LLM_API_URL")
LLM_API_KEY = os.environ.get("LLM_API_KEY")

if not LLM_API_URL or not LLM_API_KEY:
    print("ERROR: LLM_API_URL and LLM_API_KEY environment variables must be set.", file=sys.stderr)
    sys.exit(1)

from queue_db import init_db

app = FastAPI(title="SubtitleTranslator API")

# Initialize database
try:
    init_db()
except Exception as e:
    print(f"ERROR: Failed to initialize database: {e}", file=sys.stderr)
    sys.exit(1)

from api import router as api_router

@app.get("/health")
async def health():
    return {"status": "ok"}

app.include_router(api_router)

if __name__ == "__main__":
    import uvicorn
    uvicorn.run("main:app", host="0.0.0.0", port=int(APP_PORT), reload=False)
