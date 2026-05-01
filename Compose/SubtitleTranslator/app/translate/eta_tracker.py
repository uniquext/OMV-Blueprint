import threading
from typing import List

class ETATracker:
    def __init__(self, window_size: int = 5):
        self.window_size = window_size
        self.durations: List[float] = []
        self.lock = threading.Lock()

    def add_duration(self, duration: float):
        with self.lock:
            self.durations.append(duration)
            if len(self.durations) > self.window_size:
                self.durations.pop(0)

    def get_average_duration(self) -> float:
        with self.lock:
            if not self.durations:
                return 0.0
            return sum(self.durations) / len(self.durations)

eta_tracker = ETATracker()
