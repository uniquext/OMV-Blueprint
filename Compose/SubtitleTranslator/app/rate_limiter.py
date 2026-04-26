import time
import os
import threading
import logging

logger = logging.getLogger(__name__)

RPM_LIMIT = int(os.environ.get("RPM_LIMIT", "60"))
TPM_LIMIT = int(os.environ.get("TPM_LIMIT", "40000"))
BATCH_SIZE = int(os.environ.get("BATCH_SIZE", "50"))

class RateLimiter:
    def __init__(self):
        self.minute_start = time.time()
        self.tokens_this_minute = 0
        self.requests_this_minute = 0
        self.lock = threading.Lock()

    def _reset_if_needed(self):
        now = time.time()
        if now - self.minute_start >= 60:
            self.minute_start = now
            self.tokens_this_minute = 0
            self.requests_this_minute = 0

    def pre_request_check(self):
        # We sleep outside the lock to avoid blocking other threads if there were any, 
        # but in this case we have a single consumer thread mostly. 
        # Still better to calculate sleep time inside, sleep outside, then update inside.
        sleep_time = 0
        with self.lock:
            self._reset_if_needed()
            if self.requests_this_minute >= RPM_LIMIT or self.tokens_this_minute >= TPM_LIMIT:
                now = time.time()
                sleep_time = max(0.1, 60 - (now - self.minute_start))
        
        if sleep_time > 0:
            logger.warning(f"Limit reached (RPM: {self.requests_this_minute}/{RPM_LIMIT}, TPM: {self.tokens_this_minute}/{TPM_LIMIT}). Sleeping for {sleep_time:.2f}s")
            time.sleep(sleep_time)
            with self.lock:
                self.minute_start = time.time()
                self.tokens_this_minute = 0
                self.requests_this_minute = 0

        with self.lock:
            self.requests_this_minute += 1

    def post_request_update(self, total_tokens: int):
        with self.lock:
            if total_tokens is None or total_tokens <= 0:
                total_tokens = BATCH_SIZE * 50
            
            self._reset_if_needed()
            self.tokens_this_minute += total_tokens
            logger.info(f"Current usage - RPM: {self.requests_this_minute}/{RPM_LIMIT}, TPM: {self.tokens_this_minute}/{TPM_LIMIT}")

# Global singleton instance
rate_limiter = RateLimiter()
