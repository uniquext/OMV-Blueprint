import time
import threading
import logging

logger = logging.getLogger(__name__)

class RateLimiter:
    RPM_MARGIN = 100
    TPM_MARGIN = 5000

    def __init__(self):
        self._rpm_limit = None
        self._tpm_limit = None
        self._batch_size = None
        self.minute_start = time.time()
        self.tokens_this_minute = 0
        self.requests_this_minute = 0
        self.lock = threading.Lock()

    def _ensure_config(self):
        if self._rpm_limit is not None:
            return
        from config_loader import load_config
        config = load_config()
        self._rpm_limit = config["llm"]["rpm_limit"]
        self._tpm_limit = config["llm"]["tpm_limit"]
        self._batch_size = config["llm"]["batch_size"]

    @property
    def rpm_limit(self):
        self._ensure_config()
        return self._rpm_limit

    @property
    def tpm_limit(self):
        self._ensure_config()
        return self._tpm_limit

    @property
    def batch_size(self):
        self._ensure_config()
        return self._batch_size

    @property
    def rpm_threshold(self):
        return max(0, self.rpm_limit - self.RPM_MARGIN)

    @property
    def tpm_threshold(self):
        return max(0, self.tpm_limit - self.TPM_MARGIN)

    def _reset_if_needed(self):
        now = time.time()
        if now - self.minute_start >= 60:
            self.minute_start = now
            self.tokens_this_minute = 0
            self.requests_this_minute = 0

    def pre_request_check(self):
        sleep_time = 0
        with self.lock:
            self._reset_if_needed()
            if self.requests_this_minute >= self.rpm_threshold or self.tokens_this_minute >= self.tpm_threshold:
                now = time.time()
                sleep_time = max(0.1, 60 - (now - self.minute_start))

        if sleep_time > 0:
            logger.warning(f"Limit approaching (RPM: {self.requests_this_minute}/{self.rpm_limit}, TPM: {self.tokens_this_minute}/{self.tpm_limit}). Sleeping for {sleep_time:.2f}s")
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
                total_tokens = self.batch_size * 50

            self._reset_if_needed()
            self.tokens_this_minute += total_tokens
            logger.info(f"Current usage - RPM: {self.requests_this_minute}/{self.rpm_limit}, TPM: {self.tokens_this_minute}/{self.tpm_limit}")

rate_limiter = RateLimiter()
