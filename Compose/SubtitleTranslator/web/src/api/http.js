/**
 * 公共 HTTP 请求工具
 *
 * 统一处理 API 响应解析和错误提取，消除各 API 模块的重复代码。
 */

export async function handleResponse(response) {
  if (!response.ok) {
    let errorMsg = `HTTP Error: ${response.status}`
    try {
      const errorData = await response.json()
      if (errorData.message) {
        errorMsg = errorData.message
      } else if (errorData.detail) {
        if (typeof errorData.detail === 'string') {
          errorMsg = errorData.detail
        } else if (errorData.detail.message) {
          errorMsg = errorData.detail.message
        } else if (Array.isArray(errorData.detail)) {
          errorMsg = 'Validation Error: ' + JSON.stringify(errorData.detail)
        }
      }
    } catch (e) {
      // JSON 解析失败，保留通用错误信息
    }
    throw new Error(errorMsg)
  }
  const json = await response.json()
  return json.data
}
