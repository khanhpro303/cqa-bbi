export const MESSENGER_INSIGHTS_PROFILE = 'messenger_insights'

export interface ClassificationRule {
  name: string
  description: string
  severity: string
}

export interface ClassificationConfig {
  profile?: string
  rules: ClassificationRule[]
}

const messengerRules: ClassificationRule[] = [
  { name: 'Nhu cầu - Mua hàng', description: 'Khách thể hiện ý định đặt mua, chốt đơn hoặc hỏi cách mua.', severity: 'CAN_CAI_THIEN' },
  { name: 'Nhu cầu - Hỏi tồn kho', description: 'Khách hỏi sản phẩm, mẫu, màu hoặc kích thước còn hàng hay không.', severity: 'CAN_CAI_THIEN' },
  { name: 'Nhu cầu - Hỏi thông tin sản phẩm', description: 'Khách hỏi công dụng, đặc điểm, cách dùng hoặc sản phẩm phù hợp.', severity: 'CAN_CAI_THIEN' },
  { name: 'Nhu cầu - Hỏi giá/khuyến mãi', description: 'Khách hỏi giá bán, ưu đãi, phí vận chuyển hoặc chương trình khuyến mãi.', severity: 'CAN_CAI_THIEN' },
  { name: 'Nhu cầu - Hỗ trợ sau mua', description: 'Khách hỏi về đơn đã mua, giao hàng, đổi trả, bảo hành hoặc cách xử lý lỗi.', severity: 'CAN_CAI_THIEN' },
  { name: 'Feedback - Tích cực', description: 'Khách khen sản phẩm, dịch vụ, giao hàng hoặc trải nghiệm CSKH.', severity: 'CAN_CAI_THIEN' },
  { name: 'Feedback - Tiêu cực/khiếu nại', description: 'Khách chê, góp ý tiêu cực hoặc khiếu nại về sản phẩm, giá, giao hàng hay CSKH.', severity: 'NGHIEM_TRONG' },
  { name: 'Quality mess - Tiềm năng cao', description: 'Nhu cầu rõ ràng, hỏi cụ thể và có dấu hiệu sẵn sàng mua hoặc chốt đơn.', severity: 'CAN_CAI_THIEN' },
  { name: 'Quality mess - Đang tìm hiểu', description: 'Có nhu cầu liên quan sản phẩm nhưng ý định mua chưa rõ hoặc còn so sánh.', severity: 'CAN_CAI_THIEN' },
  { name: 'Quality mess - Tiềm năng thấp', description: 'Có bằng chứng rõ khách không có nhu cầu mua phù hợp. Không suy ra từ tin nhắn ngắn, khiếu nại hoặc yêu cầu hỗ trợ sau mua.', severity: 'CAN_CAI_THIEN' },
  { name: 'Quality mess - Spam', description: 'Có bằng chứng quảng cáo không liên quan, nội dung rác hoặc lặp vô nghĩa; không coi câu chào, sticker hoặc phản ánh tiêu cực là spam.', severity: 'CAN_CAI_THIEN' },
  { name: 'Quality mess - Chưa đủ dữ liệu', description: 'Chưa đủ bằng chứng để đánh giá ý định mua, ví dụ chỉ chào hỏi, gửi sticker hoặc nhắn quá ít thông tin.', severity: 'CAN_CAI_THIEN' },
]

function isRule(value: unknown): value is ClassificationRule {
  if (!value || typeof value !== 'object') return false
  const rule = value as Record<string, unknown>
  return typeof rule.name === 'string' && typeof rule.description === 'string'
}

export function parseClassificationConfig(raw: unknown): ClassificationConfig {
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw || '[]') : raw
    if (Array.isArray(parsed)) {
      return { rules: parsed.filter(isRule) }
    }
    if (parsed && typeof parsed === 'object') {
      const object = parsed as Record<string, unknown>
      return {
        profile: object.profile === MESSENGER_INSIGHTS_PROFILE ? MESSENGER_INSIGHTS_PROFILE : undefined,
        rules: Array.isArray(object.rules) ? object.rules.filter(isRule) : [],
      }
    }
  } catch {
    // Invalid config is represented as an empty legacy rules list.
  }
  return { rules: [] }
}

export function serializeClassificationConfig(config: ClassificationConfig): string {
  if (config.profile === MESSENGER_INSIGHTS_PROFILE) {
    return JSON.stringify({ profile: MESSENGER_INSIGHTS_PROFILE, rules: config.rules })
  }
  return JSON.stringify(config.rules)
}

export function isClassificationConfigValid(raw: unknown): boolean {
  return parseClassificationConfig(raw).rules.some(rule => rule.name.trim() !== '' && rule.description.trim() !== '')
}

export function createMessengerInsightsConfig(): ClassificationConfig {
  return {
    profile: MESSENGER_INSIGHTS_PROFILE,
    rules: messengerRules.map(rule => ({ ...rule })),
  }
}
