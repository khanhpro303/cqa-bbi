import type { ProductInsight, LeadQualityInsight } from './service-quality'

export interface ProductGroupingSource {
  insight_job_id?: string
  conversation_id: string
  customer_name: string
  channel_id: string
  channel_name: string
  matched_products: ProductInsight[]
  insight_at: string | null
  lead_quality: LeadQualityInsight
  summary: string
  quality_analysis_stale?: boolean
  quality_analysis?: {
    job_name?: string
    job_run_id?: string
    evaluated_at?: string
    verdict: string
    score?: number
    review?: string
    violations?: { rule_name: string; severity?: string; explanation?: string; evidence?: string; suggestion?: string }[]
  } | null
}

export interface ProductGroupingReport {
  status: 'ready' | 'pending'
  grouping_ready: boolean
  report: { from: string; to: string; generated_at: string; channel_id: string; pages: { id: string; name: string }[] }
  excluded_product_names: string[]
  product_names: string[]
  groups: { name: string; members: string[]; count: number; sources: ProductGroupingSource[] }[]
}
