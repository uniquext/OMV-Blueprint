from typing import List, Optional, Dict, Any
import os
from pydantic import BaseModel
from fastapi import APIRouter, HTTPException
import logging

import queue_db
from scanner import scan_directory
from eta_tracker import eta_tracker

logger = logging.getLogger(__name__)

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
        raise HTTPException(status_code=400, detail=f"File does not exist: {request.file_path}")
    
    try:
        task_id = queue_db.add_task(request.file_path)
        return {
            "task_id": task_id,
            "status": "queued",
            "message": "Task added to queue"
        }
    except queue_db.TaskAlreadyExistsError as e:
        # In this minimal implementation, we might not readily know the existing task_id,
        # but we can query it if needed or just return the error.
        existing_task = queue_db.get_task_by_file(request.file_path)
        existing_id = existing_task['id'] if existing_task else ""
        raise HTTPException(status_code=409, detail={"error": "This file is already in the processing queue", "task_id": existing_id})

@router.post("/scan")
async def scan_endpoint(request: ScanRequest):
    if not os.path.exists(request.dir_path) or not os.path.isdir(request.dir_path):
        raise HTTPException(status_code=400, detail=f"Directory does not exist: {request.dir_path}")
        
    task_ids = []
    
    files_to_translate = scan_directory(request.dir_path)
    for file_path in files_to_translate:
        try:
            tid = queue_db.add_task(file_path)
            task_ids.append(tid)
        except queue_db.TaskAlreadyExistsError:
            pass # Ignore already queued files during scan
                    
    if not task_ids:
        return {
            "task_ids": [],
            "count": 0,
            "message": "No files found to translate"
        }
        
    return {
        "task_ids": task_ids,
        "count": len(task_ids),
        "message": f"Scanned {len(task_ids)} files to translate, added to queue"
    }

@router.get("/progress")
async def progress_endpoint(file_path: str):
    task = queue_db.get_task_by_file(file_path)
    if not task:
        raise HTTPException(status_code=404, detail="Translation task for this file not found")
        
    percentage = 0.0
    if task['total_batches'] > 0:
        percentage = round((task['current_batch'] / task['total_batches']) * 100, 2)
        
    eta_seconds = None
    if task['status'] == 'processing':
        remaining_batches = task['total_batches'] - task['current_batch']
        if remaining_batches > 0:
            avg_duration = eta_tracker.get_average_duration()
            if avg_duration > 0:
                eta_seconds = int(remaining_batches * avg_duration)
    
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
