async function handleResponse(response) {
    if (!response.ok) {
        let errorMsg = `HTTP Error: ${response.status}`;
        try {
            const errorData = await response.json();
            if (errorData.message) {
                errorMsg = errorData.message;
            } else if (errorData.detail) {
                if (typeof errorData.detail === 'string') {
                    errorMsg = errorData.detail;
                } else if (errorData.detail.message) {
                    errorMsg = errorData.detail.message;
                }
            }
        } catch (e) {
            // json parse failed
        }
        throw new Error(errorMsg);
    }
    const json = await response.json();
    return json.data;
}

export async function getStats() {
    const response = await fetch('/api/stats');
    return handleResponse(response);
}

export async function getDebouncing() {
    const response = await fetch('/api/queue/debouncing');
    return handleResponse(response);
}

export async function getPending() {
    const response = await fetch('/api/queue/pending');
    return handleResponse(response);
}

export async function getActiveJobs() {
    const response = await fetch('/api/jobs?status=funneling&status=extracting&status=translating&status=rebuilding');
    return handleResponse(response);
}

export async function triggerScan() {
    const response = await fetch('/api/scan', {
        method: 'POST'
    });
    return handleResponse(response);
}
