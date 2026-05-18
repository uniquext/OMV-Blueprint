async function handleResponse(response) {
  if (!response.ok) {
    let errorMsg = `HTTP Error: ${response.status}`
    try {
      const errorData = await response.json()
      if (errorData.detail) {
        if (typeof errorData.detail === 'string') {
          errorMsg = errorData.detail
        } else if (errorData.detail.message) {
          errorMsg = errorData.detail.message
        } else if (Array.isArray(errorData.detail)) {
          errorMsg = 'Validation Error: ' + JSON.stringify(errorData.detail)
        }
      }
    } catch (e) {
      // json parse failed, keep generic message
    }
    throw new Error(errorMsg)
  }

  const json = await response.json()
  return json.data
}

export async function getConfig() {
  const response = await fetch('/api/config')
  return handleResponse(response)
}

export async function putConfig(payload) {
  const response = await fetch('/api/config', {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(payload)
  })
  return handleResponse(response)
}

export async function getPrompts() {
  const response = await fetch('/api/prompts')
  return handleResponse(response)
}

export async function putPrompts(payload) {
  const response = await fetch('/api/prompts', {
    method: 'PUT',
    headers: {
      'Content-Type': 'application/json'
    },
    body: JSON.stringify(payload)
  })
  return handleResponse(response)
}

export async function pollHealth() {
  try {
    const response = await fetch('/api/health')
    if (response.ok) {
      const json = await response.json()
      return json.code === 200
    }
    return false
  } catch (error) {
    return false
  }
}
