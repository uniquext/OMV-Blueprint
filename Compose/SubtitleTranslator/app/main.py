import os
import sys
import logging
from logging.handlers import RotatingFileHandler
from fastapi import FastAPI

def setup_logging():
    log_dir = "/app/logs"
    if not os.path.exists(log_dir):
        os.makedirs(log_dir, exist_ok=True)
        
    log_file = os.path.join(log_dir, "subtitle_translator.log")
    
    logger = logging.getLogger()
    logger.setLevel(logging.INFO)
    
    formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
    
    file_handler = RotatingFileHandler(log_file, maxBytes=10*1024*1024, backupCount=5)
    file_handler.setFormatter(formatter)
    logger.addHandler(file_handler)
    
    stream_handler = logging.StreamHandler(sys.stdout)
    stream_handler.setFormatter(formatter)
    logger.addHandler(stream_handler)

setup_logging()
logger = logging.getLogger(__name__)

APP_PORT = os.environ.get("APP_PORT", "9800")
LLM_API_URL = os.environ.get("LLM_API_URL")
LLM_API_KEY = os.environ.get("LLM_API_KEY")

if not LLM_API_URL or not LLM_API_KEY:
    logger.error("LLM_API_URL and LLM_API_KEY environment variables must be set.")
    sys.exit(1)

from queue_db import init_db

app = FastAPI(title="SubtitleTranslator API")

# Initialize database
try:
    init_db()
except Exception as e:
    logger.error(f"Failed to initialize database: {e}")
    sys.exit(1)

from api import router as api_router
import threading
from consumer import consumer_loop
from scheduler import start_scheduler

@app.on_event("startup")
def startup_event():
    consumer_thread = threading.Thread(target=consumer_loop, daemon=True)
    consumer_thread.start()
    
    start_scheduler()



@app.get("/health")
async def health():
    return {"status": "ok"}

app.include_router(api_router)

if __name__ == "__main__":
    import uvicorn
    uvicorn.run("main:app", host="0.0.0.0", port=int(APP_PORT), reload=False)
