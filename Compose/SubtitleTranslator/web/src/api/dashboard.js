import { handleResponse } from './http'

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

export async function getJobsByStatuses(statuses, page = 1, pageSize = 20) {
    const statusQuery = statuses.map(s => `status=${s}`).join('&');
    const response = await fetch(`/api/jobs?${statusQuery}&page=${page}&page_size=${pageSize}&sort_dir=asc`);
    return handleResponse(response);
}

export async function triggerScan() {
    const response = await fetch('/api/scan', {
        method: 'POST'
    });
    return handleResponse(response);
}
