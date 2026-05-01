"""
文件系统变动监听模块 (Watchdog)

负责监听 /media 目录下的文件创建事件，
如果是合法的媒体文件，则推入 DebounceMap 等待进一步处理。
"""
import os
import logging
from typing import List
from watchdog.observers import Observer
from watchdog.events import FileSystemEventHandler, FileCreatedEvent

from scanner.media_scanner import is_media_file

logger = logging.getLogger(__name__)

class MediaFileHandler(FileSystemEventHandler):
    """处理新媒体文件创建事件"""

    def __init__(self, debounce_map, extensions: List[str]):
        """
        Args:
            debounce_map: 管道 Layer 1 的 DebounceMap 实例
            extensions: 允许的媒体扩展名列表
        """
        super().__init__()
        self.debounce_map = debounce_map
        self.extensions = extensions

    def on_created(self, event):
        if event.is_directory:
            return

        file_path = event.src_path
        filename = os.path.basename(file_path)

        if filename.startswith(".") or filename.endswith((".part", ".tmp", ".!qB", ".crdownload")):
            return

        if is_media_file(filename, self.extensions):
            logger.info(f"Watchdog detected new media file: {file_path}")
            self.debounce_map.upsert(file_path, source="watchdog")
        else:
            logger.debug(f"Watchdog ignored non-media/hidden file: {file_path}")


def start_watchdog(watch_dir: str, debounce_map, extensions: List[str]) -> Observer:
    """
    启动监控器。

    Args:
        watch_dir: 监控目录
        debounce_map: 管道去重映射表
        extensions: 媒体扩展名列表

    Returns:
        Observer 实例 (未启动时需调用 start())
    """
    event_handler = MediaFileHandler(debounce_map, extensions)
    observer = Observer()
    observer.schedule(event_handler, watch_dir, recursive=True)
    observer.start()
    logger.info(f"Watchdog started watching {watch_dir} for extensions {extensions}")
    return observer


def stop_watchdog(observer: Observer) -> None:
    """
    安全停止监控器。

    Args:
        observer: 运行中的 Observer 实例
    """
    if observer:
        observer.stop()
        observer.join()
        logger.info("Watchdog stopped")
