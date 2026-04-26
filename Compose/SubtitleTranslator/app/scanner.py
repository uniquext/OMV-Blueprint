import os
from typing import List
import logging

logger = logging.getLogger(__name__)

def scan_directory(dir_path: str) -> List[str]:
    """
    Recursively scan the directory and return a list of .txt file paths to be translated.
    Triple filtering:
    1. Extension filtering: Must end with .txt
    2. Naming filtering: Cannot contain .zh. and cannot end with .tmp
    3. Existence filtering: Corresponding .zh.txt must not already exist
    """
    found_files = []
    
    if not os.path.exists(dir_path):
        logger.warning(f"Directory does not exist: {dir_path}")
        return found_files

    for root, _, files in os.walk(dir_path):
        for file in files:
            # 1. Extension filtering
            if not file.endswith(".txt"):
                continue
                
            # 2. Naming filtering
            if ".zh." in file or file.endswith(".tmp"):
                continue
                
            full_path = os.path.join(root, file)
            
            # 3. Existence filtering
            out_path = full_path.replace(".txt", ".zh.txt")
            if full_path.endswith(".en.txt"):
                out_path = full_path.replace(".en.txt", ".zh.txt")
                
            if os.path.exists(out_path):
                continue
                
            found_files.append(full_path)
            
    return found_files
