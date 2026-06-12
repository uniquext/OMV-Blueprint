import { handleResponse } from './http'

export async function getJobs(params = {}) {
  const queryParts = []
  if (params.page !== undefined) queryParts.push(`page=${params.page}`)
  if (params.page_size !== undefined) queryParts.push(`page_size=${params.page_size}`)

  if (params.status && Array.isArray(params.status)) {
    params.status.forEach(st => {
      if (st) queryParts.push(`status=${encodeURIComponent(st)}`)
    })
  } else if (params.status) {
    queryParts.push(`status=${encodeURIComponent(params.status)}`)
  }

  if (params.funnel_type !== undefined && params.funnel_type !== null && params.funnel_type !== '') {
    queryParts.push(`funnel_type=${params.funnel_type}`)
  }
  if (params.date_from) {
    queryParts.push(`date_from=${encodeURIComponent(params.date_from)}`)
  }
  if (params.date_to) {
    queryParts.push(`date_to=${encodeURIComponent(params.date_to)}`)
  }

  const queryStr = queryParts.length > 0 ? `?${queryParts.join('&')}` : ''
  const response = await fetch(`/api/jobs${queryStr}`)
  return handleResponse(response)
}

export async function getJobsStats() {
  const response = await fetch('/api/jobs/stats')
  return handleResponse(response)
}

export async function getLogs(lines = 200) {
  const response = await fetch(`/api/logs?lines=${lines}`)
  return handleResponse(response)
}

export async function getTaskErrors(taskId) {
  const response = await fetch(`/api/tasks/${taskId}/errors`)
  return handleResponse(response)
}

export async function retryTask(taskId) {
  const response = await fetch(`/api/tasks/${taskId}/retry`, {
    method: 'POST'
  })
  return handleResponse(response)
}
