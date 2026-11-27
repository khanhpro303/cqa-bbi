export type ProductAdvisoryImportStatus = 'running' | 'completed' | 'failed'

export type ProductAdvisoryImportItemStatus =
  | 'pending'
  | 'processing'
  | 'succeeded'
  | 'failed'
  | 'unmatched'
  | 'skipped'

export interface ProductAdvisoryImportJob {
  id: string
  file_name: string
  status: ProductAdvisoryImportStatus
  total_rows: number
  matched_count: number
  skipped_count: number
  unmatched_count: number
  succeeded_count: number
  failed_count: number
  image_count: number
  pending_image_count: number
  created_at: string
}

export interface ProductAdvisoryImportItem {
  id: string
  row_number: number
  input_web_name: string
  input_parent_code: string
  source_url: string
  parent_code: string
  web_name: string
  matched_by: string
  overwrite: boolean
  status: ProductAdvisoryImportItemStatus
  error_code: string
  error_message: string
  summary_error: string
  image_count: number
  pending_image_count: number
}

export interface ProductAdvisoryImportResponse {
  job: ProductAdvisoryImportJob | null
  items: ProductAdvisoryImportItem[]
}

export interface ProductAdvisoryImportReviewGroup {
  id: string
  parent_code: string
  web_name: string
  image_count: number
  pending_image_count: number
}

export function importReviewGroups(items: ProductAdvisoryImportItem[]): ProductAdvisoryImportReviewGroup[] {
  return items
    .filter((item) => item.status === 'succeeded' && item.image_count > 0)
    .map((item) => ({
      id: item.id,
      parent_code: item.parent_code,
      web_name: item.web_name,
      image_count: item.image_count,
      pending_image_count: item.pending_image_count,
    }))
}
