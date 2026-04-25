from typing import List, Optional, Dict, Any
import os
from pydantic import BaseModel
from fastapi import APIRouter, HTTPException

import queue_db

router = APIRouter()

class TranslateRequest(BaseModel):
    file_path: str

class ScanRequest(BaseModel):
    dir_path: str

class TaskResponse(BaseModel):
    task_id: str
    status: str
    message: str
    
class ErrorResponse(BaseModel):
    error: str

@router.post("/translate")
async def translate_endpoint(request: TranslateRequest):
    # Verify file existence (using the mapped volume path, here absolute for container)
    if not os.path.isfile(request.file_path):
        raise HTTPException(status_code=400, detail=f"文件不存在: {request.file_path}")
    
    try:
        task_id = queue_db.add_task(request.file_path)
        return {
            "task_id": task_id,
            "status": "queued",
            "message": "任务已加入队列"
        }
    except queue_db.TaskAlreadyExistsError as e:
        # In this minimal implementation, we might not readily know the existing task_id,
        # but we can query it if needed or just return the error.
        existing_task = queue_db.get_task_by_file(request.file_path)
        existing_id = existing_task['id'] if existing_task else ""
        raise HTTPException(status_code=409, detail={"error": "该文件已在处理队列中", "task_id": existing_id})


@router.post("/test_translate")
async def test_translate_endpoint(request: TranslateRequest):
    import queue_db
    import translator
    if not os.path.isfile(request.file_path):
        raise HTTPException(status_code=400, detail=f"文件不存在: {request.file_path}")
    
    try:
        task_id = queue_db.add_task(request.file_path)
    except queue_db.TaskAlreadyExistsError:
        task = queue_db.get_task_by_file(request.file_path)
        task_id = task["id"]
        
    with queue_db.sqlite3.connect(queue_db.DB_PATH) as conn:
        conn.execute("UPDATE task SET status = 'processing' WHERE id = ?", (task_id,))
        conn.commit()
        
    translator.translate_file(task_id, request.file_path)
    return {"message": "翻译完成", "task_id": task_id}


@router.post("/scan")
async def scan_endpoint(request: ScanRequest):
    if not os.path.exists(request.dir_path) or not os.path.isdir(request.dir_path):
        raise HTTPException(status_code=400, detail=f"目录不存在: {request.dir_path}")
        
    task_ids = []
    # Basic scan for .txt files
    for root, _, files in os.walk(request.dir_path):
        for file in files:
            if file.endswith('.txt'):
                file_path = os.path.join(root, file)
                try:
                    tid = queue_db.add_task(file_path)
                    task_ids.append(tid)
                except queue_db.TaskAlreadyExistsError:
                    pass # Ignore already queued files during scan
                    
    if not task_ids:
        return {
            "task_ids": [],
            "count": 0,
            "message": "未发现待翻译文件"
        }
        
    return {
        "task_ids": task_ids,
        "count": len(task_ids),
        "message": f"已扫描到 {len(task_ids)} 个待翻译文件，已加入队列"
    }

@router.get("/progress")
async def progress_endpoint(file_path: str):
    task = queue_db.get_task_by_file(file_path)
    if not task:
        raise HTTPException(status_code=404, detail="未找到该文件的翻译任务")
        
    percentage = 0.0
    if task['total_batches'] > 0:
        percentage = round((task['current_batch'] / task['total_batches']) * 100, 2)
        
    # ETA is calculated later (phase 10), so we use None for now
    eta_seconds = None
    
    return {
        "task_id": task['id'],
        "file_path": task['file_path'],
        "status": task['status'],
        "progress": task['progress'],
        "percentage": percentage,
        "current_batch": task['current_batch'],
        "total_batches": task['total_batches'],
        "eta_seconds": eta_seconds
    }

@router.get("/queue")
async def queue_endpoint():
    return queue_db.get_queue_stats()
