import os
import json
import logging

logger = logging.getLogger(__name__)

SYSTEM_PROMPT_PATH = "/app/prompts/system_prompt.txt"
GLOSSARY_PATH = "/app/prompts/glossary.json"

def load_system_prompt() -> str:
    if not os.path.isfile(SYSTEM_PROMPT_PATH):
        raise FileNotFoundError(f"System prompt file not found: {SYSTEM_PROMPT_PATH}")
    with open(SYSTEM_PROMPT_PATH, "r", encoding="utf-8") as f:
        return f.read()

def load_glossary() -> dict:
    if not os.path.isfile(GLOSSARY_PATH):
        logger.info(f"Glossary file missing at {GLOSSARY_PATH}, using empty dictionary.")
        return {}
    
    with open(GLOSSARY_PATH, "r", encoding="utf-8") as f:
        # Expected to raise json.JSONDecodeError on invalid JSON
        return json.load(f)
