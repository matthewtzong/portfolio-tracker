import { useCallback, useEffect, useState } from 'react'
import { apiRequest } from '../lib/api'
import { TimeSeriesChart } from './TimeSeriesChart'

// Holding in JSON format.
interface Holding {
  accountId: string
  accountName: string
  symbol: string
  quantity: number
  valueCents: number
}

// Holdings response in JSON format.
interface HoldingsResponse {
  holdings: Holding[]
}

// Snapshot data point for charts in JSON format.
interface SnapshotDataPoint {
  date: string
  portfolioValueCents: number
}

// Snapshots response in JSON format.
interface SnapshotsResponse {
  daily: SnapshotDataPoint[]
  monthly: SnapshotDataPoint[]
}

// Holding data point for charts in JSON format.
interface HoldingDataPoint {
  date: string
  accountId: string
  accountName?: string
  symbol: string
  quantity?: number
  valueCents: number
}

// Holdings history response in JSON format.
interface HoldingsHistoryResponse {
  daily: HoldingDataPoint[]
}

// Yearly portfolio summary by account.
interface YearlyPortfolioAccount {
  accountId: string
  portfolioValueCents: number
}

interface YearlyPortfolioSummaryResponse {
  year: number
  byAccount: YearlyPortfolioAccount[]
}

// Contribution-adjusted portfolio performance (Modified Dietz).
interface PerformanceResponse {
  range: string
  startDate: string
  endDate: string
  startValueCents: number
  endValueCents: number
  netContributionsCents: number
  gainCents: number
  returnBps: number
  method: string
}

interface PerformanceSummaryResponse {
  dayOverDay?: PerformanceResponse
  monthOverMonth?: PerformanceResponse
}

const START_MONTH = '2026-03'
const START_YEAR = 2026

// Selected item for the portfolio.
type SelectedItem =
  | null
  | { type: 'total' }
  | { type: 'account'; accountId: string; accountName: string }
  | { type: 'holding'; accountId: string; accountName: string; symbol: string }

// Formats the currency.
function formatCurrency(cents: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
  }).format(cents / 100)
}

function formatSnapshotDate(date: string): string {
  const parsed = new Date(`${date}T12:00:00`)
  if (Number.isNaN(parsed.getTime())) return date
  return parsed.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

function formatGainPeriod(perf?: PerformanceResponse): string | null {
  if (!perf?.startDate || !perf?.endDate) return null
  return `${formatSnapshotDate(perf.startDate)} → ${formatSnapshotDate(perf.endDate)}`
}

function formatGainDelta(perf?: PerformanceResponse): string {
  if (!perf) return '—'
  const sign = perf.gainCents >= 0 ? '+' : ''
  return `${sign}${formatCurrency(perf.gainCents)} (${sign}${(perf.returnBps / 100).toFixed(2)}%)`
}

function formatReturnPct(bps: number): string {
  const pct = bps / 100
  const sign = pct > 0 ? '+' : ''
  return `${sign}${pct.toFixed(2)}%`
}

function formatSignedCurrency(cents: number): string {
  const sign = cents > 0 ? '+' : cents < 0 ? '-' : ''
  return `${sign}${formatCurrency(Math.abs(cents))}`
}

export function Portfolio() {
  const [holdings, setHoldings] = useState<Holding[]>([])
  const [holdingsLoading, setHoldingsLoading] = useState(false)
  const [holdingsError, setHoldingsError] = useState<string | null>(null)

  const [snapshots, setSnapshots] = useState<SnapshotsResponse | null>(null)
  const [snapshotsLoading, setSnapshotsLoading] = useState(false)

  const [selected, setSelected] = useState<SelectedItem>(null)

  const currentDate = new Date()
  const lastMonth = new Date(currentDate.getFullYear(), currentDate.getMonth() - 1, 1)
  const [exportMonth, setExportMonth] = useState(() => {
    const lm = `${lastMonth.getFullYear()}-${String(lastMonth.getMonth() + 1).padStart(2, '0')}`
    return lm < START_MONTH ? START_MONTH : lm
  })

  const [historyDaily, setHistoryDaily] = useState<HoldingDataPoint[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)

  const [accountMonthly, setAccountMonthly] = useState<SnapshotDataPoint[]>([])
  const [accountMonthlyLoading, setAccountMonthlyLoading] = useState(false)

  const [yearlySummaryYear, setYearlySummaryYear] = useState<string>('')
  const [yearlySummary, setYearlySummary] = useState<YearlyPortfolioSummaryResponse | null>(null)
  const [yearlySummaryLoading, setYearlySummaryLoading] = useState(false)
  const [yearlySummaryError, setYearlySummaryError] = useState<string | null>(null)

  const [performanceSummary, setPerformanceSummary] = useState<PerformanceSummaryResponse | null>(
    null,
  )
  const [summaryLoading, setSummaryLoading] = useState(false)
  const [summaryError, setSummaryError] = useState<string | null>(null)

  const [perfRange, setPerfRange] = useState<'all' | '1y' | 'ytd'>('all')
  const [performance, setPerformance] = useState<PerformanceResponse | null>(null)
  const [performanceLoading, setPerformanceLoading] = useState(false)
  const [performanceError, setPerformanceError] = useState<string | null>(null)

  // const [uploadMessage, setUploadMessage] = useState<{
  //   text: string
  //   type: 'success' | 'error'
  // } | null>(null)
  // const [uploadLoading, setUploadLoading] = useState(false)
  // const [fidelityAccountType, setFidelityAccountType] = useState<'brokerage' | 'roth_ira'>(
  //   'brokerage',
  // )

  // Loads the holdings.
  const loadHoldings = useCallback(async () => {
    setHoldingsLoading(true)
    setHoldingsError(null)
    try {
      const data = await apiRequest<HoldingsResponse>('/api/portfolio/holdings')
      setHoldings(data.holdings ?? [])
    } catch (err) {
      setHoldingsError(err instanceof Error ? err.message : 'Failed to load holdings')
    } finally {
      setHoldingsLoading(false)
    }
  }, [])

  // Loads the snapshots.
  const loadSnapshots = useCallback(async () => {
    setSnapshotsLoading(true)
    try {
      const data = await apiRequest<SnapshotsResponse>('/api/portfolio/snapshots')
      setSnapshots({
        daily: data.daily ?? [],
        monthly: data.monthly ?? [],
      })
    } catch (err) {
      // Ignored for now as snapshotsError was removed for design simplicity
    } finally {
      setSnapshotsLoading(false)
    }
  }, [])

  // Loads contribution-adjusted day/month performance (Modified Dietz).
  const loadPerformanceSummary = useCallback(async () => {
    setSummaryLoading(true)
    setSummaryError(null)
    try {
      const data = await apiRequest<PerformanceSummaryResponse>(
        '/api/portfolio/performance/summary',
      )
      setPerformanceSummary(data)
    } catch (err) {
      setPerformanceSummary(null)
      setSummaryError(
        err instanceof Error ? err.message : 'Failed to load performance summary',
      )
    } finally {
      setSummaryLoading(false)
    }
  }, [])

  const loadPerformance = useCallback(async (range: 'all' | '1y' | 'ytd') => {
    setPerformanceLoading(true)
    setPerformanceError(null)
    try {
      const data = await apiRequest<PerformanceResponse>(
        `/api/portfolio/performance?range=${range}`,
      )
      setPerformance(data)
    } catch (err) {
      setPerformance(null)
      setPerformanceError(
        err instanceof Error ? err.message : 'Failed to load performance',
      )
    } finally {
      setPerformanceLoading(false)
    }
  }, [])

  // Loads the holdings and snapshots on mount.
  useEffect(() => {
    loadHoldings()
    loadSnapshots()
    loadPerformanceSummary()
  }, [loadHoldings, loadSnapshots, loadPerformanceSummary])

  useEffect(() => {
    loadPerformance(perfRange)
  }, [loadPerformance, perfRange])

  // Loads yearly portfolio summary by account.
  const loadYearlySummary = useCallback(async () => {
    if (!yearlySummaryYear) {
      setYearlySummary(null)
      setYearlySummaryError(null)
      setYearlySummaryLoading(false)
      return
    }
    setYearlySummaryLoading(true)
    setYearlySummaryError(null)
    try {
      const res = await apiRequest<YearlyPortfolioSummaryResponse>(
        `/api/portfolio/summary/yearly?year=${encodeURIComponent(yearlySummaryYear)}`,
      )
      setYearlySummary(res)
    } catch (err) {
      setYearlySummaryError(err instanceof Error ? err.message : 'Failed to load yearly summary')
      setYearlySummary(null)
    } finally {
      setYearlySummaryLoading(false)
    }
  }, [yearlySummaryYear])

  useEffect(() => {
    void loadYearlySummary()
  }, [loadYearlySummary])

  // Loads the holdings and snapshots when the selected item changes.
  useEffect(() => {
    if (!selected || selected.type === 'total') {
      setHistoryDaily([])
      setAccountMonthly([])
      return
    }
    // Loads the holdings history for an account.
    if (selected.type === 'account') {
      setHistoryLoading(true)
      apiRequest<HoldingsHistoryResponse>(
        `/api/portfolio/holdings/history?accountId=${encodeURIComponent(selected.accountId)}`,
      )
        .then((data) => setHistoryDaily(data.daily))
        .catch(() => setHistoryDaily([]))
        .finally(() => setHistoryLoading(false))
      setAccountMonthlyLoading(true)
      apiRequest<SnapshotsResponse>(
        `/api/portfolio/snapshots?accountId=${encodeURIComponent(selected.accountId)}`,
      )
        .then((data) => setAccountMonthly(data.monthly ?? []))
        .catch(() => setAccountMonthly([]))
        .finally(() => setAccountMonthlyLoading(false))
      return
    }
    // Loads the holdings history for a holding.
    if (selected.type === 'holding') {
      setAccountMonthly([])
      setHistoryLoading(true)
      apiRequest<HoldingsHistoryResponse>(
        `/api/portfolio/holdings/history?symbol=${encodeURIComponent(selected.symbol)}`,
      )
        .then((data) => setHistoryDaily(data.daily))
        .catch(() => setHistoryDaily([]))
        .finally(() => setHistoryLoading(false))
      return
    }
  }, [selected])

  // Calculates the total portfolio value
  const holdingsList = holdings ?? []
  const totalPortfolioValue = holdingsList.reduce((sum, holding) => sum + holding.valueCents, 0)

  // Calculates the holdings by account.
  const holdingsByAccount = holdingsList.reduce(
    (accountMap, holding) => {
      if (!accountMap[holding.accountId]) {
        accountMap[holding.accountId] = {
          accountName: holding.accountName,
          holdings: [],
          totalValue: 0,
        }
      }
      accountMap[holding.accountId].holdings.push(holding)
      accountMap[holding.accountId].totalValue += holding.valueCents
      return accountMap
    },
    {} as Record<string, { accountName: string; holdings: Holding[]; totalValue: number }>,
  )

  // Gets the snapshots for the total portfolio.
  const totalDailySeries = snapshots?.daily ?? []
  const totalMonthlySeries = (snapshots?.monthly ?? []).slice(-12)
  const totalDailyChartData = totalDailySeries.map((snapshot) => ({
    date: snapshot.date,
    value: snapshot.portfolioValueCents / 100,
  }))

  const totalMonthlyChartData = totalMonthlySeries.map((snapshot) => ({
    date: snapshot.date,
    value: snapshot.portfolioValueCents / 100,
  }))

  // Gets the daily snapshots for the selected account.
  const accountDailySeries = (() => {
    if (!selected || selected.type !== 'account' || !historyDaily.length) return []
    const byDate: Record<string, number> = {}
    historyDaily.forEach((holding) => {
      byDate[holding.date] = (byDate[holding.date] ?? 0) + holding.valueCents
    })
    return Object.entries(byDate)
      .map(([date, valueCents]) => ({ date, valueCents }))
      .sort((a, b) => a.date.localeCompare(b.date))
  })()

  // Gets the daily snapshots for the selected holding.
  const holdingDailySeries = (() => {
    if (!selected || selected.type !== 'holding' || !historyDaily.length) {
      return []
    }
    const byDate: Record<string, number> = {}
    historyDaily.forEach((holding) => {
      byDate[holding.date] = (byDate[holding.date] ?? 0) + holding.valueCents
    })
    return Object.entries(byDate)
      .map(([date, valueCents]) => ({ date, valueCents }))
      .sort((a, b) => a.date.localeCompare(b.date))
  })()

  // Returns the label for the selected item.
  const selectedLabel = (() => {
    if (selected === null) {
      return null
    }
    if (selected.type === 'total') {
      return 'Total portfolio'
    }
    if (selected.type === 'account') {
      return selected.accountName
    }
    return `${selected.symbol} (${selected.accountName})`
  })()

  // Exports portfolio snapshots for a month as CSV.
  const handleExportSnapshots = async () => {
    try {
      const { supabase } = await import('../lib/supabase')
      const {
        data: { session },
      } = await supabase.auth.getSession()
      const API_URL = import.meta.env.VITE_API_URL || ''
      const response = await fetch(
        `${API_URL}/api/export/portfolio/snapshots?month=${exportMonth}`,
        {
          headers: {
            Authorization: `Bearer ${session?.access_token}`,
          },
        },
      )

      if (!response.ok) {
        throw new Error('Failed to export snapshots')
      }

      // Downloads the CSV file.
      const blob = await response.blob()
      const objectURL = window.URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = objectURL
      anchor.download = `portfolio-snapshots-${exportMonth}.csv`
      document.body.appendChild(anchor)
      anchor.click()
      window.URL.revokeObjectURL(objectURL)
      document.body.removeChild(anchor)
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to export snapshots')
    }
  }

  // Exports portfolio holdings for a month as CSV.
  const handleExportHoldings = async () => {
    try {
      const { supabase } = await import('../lib/supabase')
      const {
        data: { session },
      } = await supabase.auth.getSession()
      const API_URL = import.meta.env.VITE_API_URL || ''
      const response = await fetch(
        `${API_URL}/api/export/portfolio/holdings?month=${exportMonth}`,
        {
          headers: {
            Authorization: `Bearer ${session?.access_token}`,
          },
        },
      )

      if (!response.ok) {
        throw new Error('Failed to export holdings')
      }

      // Downloads the CSV file.
      const blob = await response.blob()
      const objectURL = window.URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = objectURL
      anchor.download = `portfolio-holdings-${exportMonth}.csv`
      document.body.appendChild(anchor)
      anchor.click()
      window.URL.revokeObjectURL(objectURL)
      document.body.removeChild(anchor)
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to export holdings')
    }
  }

  // const handleFidelityUpload = async (
  //   e: React.ChangeEvent<HTMLInputElement>,
  //   type: 'statement' | 'holdings',
  // ) => {
  //   const file = e.target.files?.[0]
  //   if (!file) return

  //   setUploadLoading(true)
  //   setUploadMessage(null)

  //   // Uploads the file.
  //   try {
  //     const { supabase } = await import('../lib/supabase')
  //     const {
  //       data: { session },
  //     } = await supabase.auth.getSession()
  //     const formData = new FormData()
  //     formData.append('file', file)
  //     formData.append('accountType', fidelityAccountType)

  //     const API_URL = import.meta.env.VITE_API_URL || ''
  //     const endpoint =
  //       type === 'statement' ? '/api/fidelity/upload-statement' : '/api/fidelity/upload-holdings'

  //     const response = await fetch(`${API_URL}${endpoint}`, {
  //       method: 'POST',
  //       headers: {
  //         Authorization: `Bearer ${session?.access_token}`,
  //       },
  //       body: formData,
  //     })

  //     // Parses the response.
  //     const result = await response.json()
  //     if (!response.ok) {
  //       throw new Error(result.error || 'Upload failed')
  //     }

  //     // Sets the upload message.
  //     setUploadMessage({ text: result.message, type: 'success' })
  //     await loadHoldings()
  //     await loadSnapshots()
  //   } catch (err) {
  //     setUploadMessage({
  //       text: err instanceof Error ? err.message : 'Upload failed',
  //       type: 'error',
  //     })
  //   } finally {
  //     setUploadLoading(false)
  //     e.target.value = ''
  //   }
  // }

  // Generates month options for export dropdown (last 13 months including current).
  const generateMonthOptions = () => {
    const monthOptions: string[] = []
    const now = new Date()
    const floor = new Date(Date.UTC(START_YEAR, 2, 1))
    const retentionFloor = new Date(Date.UTC(now.getFullYear() - 1, 0, 1))
    const startMonthDate = new Date(Math.max(floor.getTime(), retentionFloor.getTime()))
    const cursor = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1))
    while (cursor >= startMonthDate) {
      const y = cursor.getUTCFullYear()
      const m = String(cursor.getUTCMonth() + 1).padStart(2, '0')
      monthOptions.push(`${y}-${m}`)
      cursor.setUTCMonth(cursor.getUTCMonth() - 1)
    }
    return monthOptions
  }

  // Returns the portfolio page.
  return (
    <div className="max-w-6xl mx-auto py-10 px-6">
      <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-6 mb-10">
        <h1 className="text-3xl font-bold text-white tracking-tight">Portfolio</h1>
        <div className="flex flex-wrap items-center gap-3">
          <select
            value={exportMonth}
            onChange={(e) => setExportMonth(e.target.value)}
            className="bg-zinc-900 border border-border text-zinc-100 rounded-full px-5 py-2.5 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary transition-all cursor-pointer"
          >
            {generateMonthOptions().map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
          <button
            type="button"
            onClick={handleExportSnapshots}
            className="px-6 py-2.5 bg-zinc-100 text-background text-sm font-bold rounded-full hover:bg-white transition-all shadow-lg active:scale-95"
          >
            Export Snapshots
          </button>
          <button
            type="button"
            onClick={handleExportHoldings}
            className="px-6 py-2.5 bg-primary text-background text-sm font-bold rounded-full hover:bg-green-400 transition-all shadow-lg active:scale-95"
          >
            Export Holdings
          </button>
        </div>
      </div>

      {/* Portfolio value + short-term contribution-adjusted gains */}
      <div className="mb-8 bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl relative overflow-hidden group">
        {holdingsError && (
          <p className="text-sm text-red-400 mb-4 font-medium">Error: {holdingsError}</p>
        )}
        {holdingsLoading && !holdings.length ? (
          <div className="h-24 bg-zinc-800 animate-pulse rounded-2xl" />
        ) : holdings.length === 0 && !holdingsError ? (
          <p className="text-sm text-zinc-500 font-medium">
            No holdings. Connect an account to see positions.
          </p>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6 items-stretch">
            <div>
              <p className="text-zinc-400 text-sm font-medium mb-1">Portfolio value today</p>
              <p className="text-5xl font-bold text-white tracking-tighter">
                {formatCurrency(totalPortfolioValue)}
              </p>
            </div>
            <div>
              <p className="text-[10px] font-bold text-zinc-500 uppercase tracking-widest mb-2">
                Day over day
              </p>
              {summaryLoading ? (
                <div className="h-8 w-40 bg-zinc-800 animate-pulse rounded-lg" />
              ) : (
                <>
                  <p
                    className={`text-xl font-bold ${
                      (performanceSummary?.dayOverDay?.gainCents ?? 0) >= 0
                        ? 'text-green-400'
                        : 'text-red-400'
                    }`}
                  >
                    {formatGainDelta(performanceSummary?.dayOverDay)}
                  </p>
                  {formatGainPeriod(performanceSummary?.dayOverDay) && (
                    <p className="text-[11px] text-zinc-500 mt-1">
                      {formatGainPeriod(performanceSummary?.dayOverDay)}
                    </p>
                  )}
                </>
              )}
            </div>
            <div className="relative h-full min-h-[7rem] pb-11">
              <p className="text-[10px] font-bold text-zinc-500 uppercase tracking-widest mb-2">
                Month over month
              </p>
              {summaryLoading ? (
                <div className="h-8 w-40 bg-zinc-800 animate-pulse rounded-lg" />
              ) : (
                <>
                  <p
                    className={`text-xl font-bold ${
                      (performanceSummary?.monthOverMonth?.gainCents ?? 0) >= 0
                        ? 'text-green-400'
                        : 'text-red-400'
                    }`}
                  >
                    {formatGainDelta(performanceSummary?.monthOverMonth)}
                  </p>
                  {formatGainPeriod(performanceSummary?.monthOverMonth) && (
                    <p className="text-[11px] text-zinc-500 mt-1">
                      {formatGainPeriod(performanceSummary?.monthOverMonth)}
                    </p>
                  )}
                </>
              )}
              <div className="absolute bottom-0 right-0">
                <div className="bg-primary/10 border border-primary/20 rounded-full px-3 py-1 flex items-center gap-1.5">
                  <span className="w-1.5 h-1.5 rounded-full bg-primary animate-pulse" />
                  <span className="text-xs font-bold text-primary">Today&apos;s Value</span>
                </div>
              </div>
            </div>
          </div>
        )}
        {summaryError && !summaryLoading && holdings.length > 0 && (
          <p className="text-xs text-zinc-600 mt-4">
            {summaryError.includes('404') || summaryError.toLowerCase().includes('no portfolio')
              ? 'Day/month gain metrics appear after nightly sync has snapshot and investment-transaction data.'
              : summaryError}
          </p>
        )}
      </div>

      {/* Contribution-adjusted performance by range */}
      <div className="mb-8 bg-card border border-border rounded-4xl p-8 shadow-2xl">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-6">
          <div>
            <h2 className="text-xl font-bold text-white tracking-tight">Performance</h2>
            <p className="text-zinc-500 text-sm mt-1">
              Gain and return excluding contributions and withdrawals.
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => setPerfRange('all')}
              className={`px-4 py-2 text-sm font-bold rounded-full transition-all ${
                perfRange === 'all'
                  ? 'bg-primary text-background'
                  : 'bg-zinc-900 text-zinc-300 border border-border hover:border-zinc-500'
              }`}
            >
              All time
            </button>
            <button
              type="button"
              onClick={() => setPerfRange('1y')}
              className={`px-4 py-2 text-sm font-bold rounded-full transition-all ${
                perfRange === '1y'
                  ? 'bg-primary text-background'
                  : 'bg-zinc-900 text-zinc-300 border border-border hover:border-zinc-500'
              }`}
            >
              1 year
            </button>
            <button
              type="button"
              onClick={() => setPerfRange('ytd')}
              className={`px-4 py-2 text-sm font-bold rounded-full transition-all ${
                perfRange === 'ytd'
                  ? 'bg-primary text-background'
                  : 'bg-zinc-900 text-zinc-300 border border-border hover:border-zinc-500'
              }`}
            >
              YTD
            </button>
          </div>
        </div>
        {performanceLoading && (
          <div className="h-16 w-64 bg-zinc-800 animate-pulse rounded-lg" />
        )}
        {performanceError && !performanceLoading && (
          <p className="text-sm text-zinc-500 font-medium">
            {performanceError.includes('404') || performanceError.toLowerCase().includes('no portfolio')
              ? 'Not enough snapshot or investment-transaction data yet. Performance appears after nightly sync.'
              : performanceError}
          </p>
        )}
        {performance && !performanceLoading && (
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-6">
            <div>
              <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide mb-1">
                Return
              </p>
              <p
                className={`text-3xl font-bold tracking-tight ${
                  performance.returnBps >= 0 ? 'text-primary' : 'text-red-400'
                }`}
              >
                {formatReturnPct(performance.returnBps)}
              </p>
            </div>
            <div>
              <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide mb-1">
                Gain
              </p>
              <p
                className={`text-3xl font-bold tracking-tight ${
                  performance.gainCents >= 0 ? 'text-white' : 'text-red-400'
                }`}
              >
                {formatSignedCurrency(performance.gainCents)}
              </p>
            </div>
            <div>
              <p className="text-zinc-500 text-xs font-medium uppercase tracking-wide mb-1">
                Net contributions
              </p>
              <p className="text-3xl font-bold text-zinc-300 tracking-tight">
                {formatSignedCurrency(performance.netContributionsCents)}
              </p>
            </div>
            <p className="sm:col-span-3 text-xs text-zinc-600">
              {performance.startDate} → {performance.endDate} · Modified Dietz · start{' '}
              {formatCurrency(performance.startValueCents)} · end{' '}
              {formatCurrency(performance.endValueCents)}
            </p>
          </div>
        )}
      </div>

      {/* Fidelity Manual Uploads
      <div className="mb-8 p-8 bg-zinc-900 border border-border rounded-4xl shadow-xl relative overflow-hidden group">
        <div className="absolute -right-4 -top-4 w-24 h-24 bg-blue-500/5 rounded-full blur-2xl group-hover:bg-blue-500/10 transition-all" />
        <div className="relative">
          <div className="flex items-center justify-between mb-6">
            <div>
              <h2 className="text-xl font-bold text-white mb-1">Fidelity Integration</h2>
              <p className="text-zinc-500 text-sm font-medium">
                Manually track Fidelity performance by uploading statements or current holdings.
              </p>
            </div>
          </div>

          <div className="flex flex-col sm:flex-row sm:items-center gap-3 mb-6">
            <span className="text-sm font-bold text-zinc-400 uppercase tracking-wider">
              Upload for
            </span>
            <div className="inline-flex rounded-full border border-border bg-zinc-800/50 p-1">
              <button
                type="button"
                onClick={() => setFidelityAccountType('brokerage')}
                disabled={uploadLoading}
                className={`px-4 py-1.5 rounded-full text-sm font-bold transition-all ${
                  fidelityAccountType === 'brokerage'
                    ? 'bg-primary text-background'
                    : 'text-zinc-400 hover:text-white'
                }`}
              >
                Brokerage
              </button>
              <button
                type="button"
                onClick={() => setFidelityAccountType('roth_ira')}
                disabled={uploadLoading}
                className={`px-4 py-1.5 rounded-full text-sm font-bold transition-all ${
                  fidelityAccountType === 'roth_ira'
                    ? 'bg-primary text-background'
                    : 'text-zinc-400 hover:text-white'
                }`}
              >
                Roth IRA
              </button>
            </div>
          </div>

          <div className="grid gap-4 md:grid-cols-2">
            <div className="relative">
              <input
                type="file"
                accept=".csv"
                onChange={(e) => handleFidelityUpload(e, 'statement')}
                disabled={uploadLoading}
                id="statement-upload"
                className="hidden"
              />
              <label
                htmlFor="statement-upload"
                className={`flex flex-col items-center justify-center p-6 border-2 border-dashed border-border rounded-3xl cursor-pointer hover:border-blue-500/50 hover:bg-blue-500/5 transition-all ${uploadLoading ? 'opacity-50 pointer-events-none' : ''}`}
              >
                <span className="text-white font-bold mb-1">Upload Monthly Statement</span>
                <span className="text-zinc-500 text-xs text-center font-medium">
                  Format: StatementMMDDYYYY.csv
                </span>
              </label>
            </div>

            <div className="relative">
              <input
                type="file"
                accept=".csv"
                onChange={(e) => handleFidelityUpload(e, 'holdings')}
                disabled={uploadLoading}
                id="holdings-upload"
                className="hidden"
              />
              <label
                htmlFor="holdings-upload"
                className={`flex flex-col items-center justify-center p-6 border-2 border-dashed border-border rounded-3xl cursor-pointer hover:border-emerald-500/50 hover:bg-emerald-500/5 transition-all ${uploadLoading ? 'opacity-50 pointer-events-none' : ''}`}
              >
                <span className="text-white font-bold mb-1">Upload Current Holdings</span>
                <span className="text-zinc-500 text-xs text-center font-medium">
                  Format: Portfolio_Positions_Mon-DD-YYYY.csv
                </span>
              </label>
            </div>
          </div>

          {uploadMessage && (
            <div
              className={`mt-6 p-4 rounded-2xl text-sm font-bold border ${
                uploadMessage.type === 'success'
                  ? 'bg-primary/10 text-primary border-primary/20'
                  : 'bg-red-500/10 text-red-500 border-red-500/20'
              }`}
            >
              {uploadMessage.text}
            </div>
          )}
        </div>
      </div> */}

      {/* By account and by holding — selectable */}
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-8">
        <div className="lg:col-span-12">
          <div className="bg-card border border-border rounded-4xl overflow-hidden shadow-2xl">
            <div className="p-8 border-b border-border">
              <h2 className="text-xl font-bold text-white">By account and position</h2>
            </div>
            {holdingsList.length === 0 && (
              <div className="p-10 text-center">
                <p className="text-zinc-500 font-medium italic">
                  Connect an account to see breakdown.
                </p>
              </div>
            )}
            {holdingsList.length > 0 && (
              <div className="p-2 space-y-2">
                {/* Clickable total row - show if holdings exist OR if snapshots exist */}
                {(holdingsList.length > 0 || (snapshots?.daily && snapshots.daily.length > 0)) && (
                  <button
                    type="button"
                    onClick={() =>
                      setSelected(selected?.type === 'total' ? null : { type: 'total' })
                    }
                    className={`w-full text-left p-6 rounded-3xl transition-all duration-300 group ${
                      selected?.type === 'total'
                        ? 'bg-zinc-800 border border-zinc-700'
                        : 'hover:bg-zinc-800/50 border border-transparent'
                    }`}
                  >
                    <div className="flex items-center justify-between">
                      <div className="flex items-center gap-4">
                        <div
                          className={`w-12 h-12 rounded-2xl flex items-center justify-center transition-colors ${selected?.type === 'total' ? 'bg-primary text-background' : 'bg-zinc-800 text-zinc-400'}`}
                        >
                          <span className="font-bold text-lg">Σ</span>
                        </div>
                        <div>
                          <span className="font-bold text-white text-lg">Total portfolio</span>
                          <div className="text-xs text-zinc-500 font-medium mt-0.5">
                            30-day and 12-month performance
                          </div>
                        </div>
                      </div>
                      <span className="text-xl font-bold text-white">
                        {formatCurrency(totalPortfolioValue)}
                      </span>
                    </div>
                  </button>
                )}

                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-2 px-2 pb-2">
                  {Object.entries(holdingsByAccount).map(([accountId, accountData]) => (
                    <div key={accountId} className="flex flex-col gap-1">
                      {/* Clickable account row */}
                      <button
                        type="button"
                        onClick={() =>
                          setSelected(
                            selected?.type === 'account' && selected.accountId === accountId
                              ? null
                              : {
                                  type: 'account',
                                  accountId,
                                  accountName: accountData.accountName,
                                },
                          )
                        }
                        className={`w-full text-left p-5 rounded-3xl border transition-all duration-300 ${
                          selected?.type === 'account' && selected.accountId === accountId
                            ? 'bg-zinc-800 border-zinc-700'
                            : 'bg-zinc-900 border-border hover:border-zinc-700 hover:bg-zinc-800/40'
                        }`}
                      >
                        <div className="flex items-center justify-between mb-1">
                          <span className="font-bold text-white truncate mr-2">
                            {accountData.accountName}
                          </span>
                          <span className="text-zinc-500 text-xs font-bold">ACC</span>
                        </div>
                        <span className="text-lg font-bold text-primary">
                          {formatCurrency(accountData.totalValue)}
                        </span>
                      </button>
                      {/* Holdings within account - show mini list only when selected (account or holding within account) */}
                      {((selected?.type === 'account' && selected.accountId === accountId) ||
                        (selected?.type === 'holding' && selected.accountId === accountId)) && (
                        <div className="space-y-1 mt-1">
                          {accountData.holdings.map((h, idx) => {
                            const isSelectedHolding =
                              selected?.type === 'holding' &&
                              selected.accountId === h.accountId &&
                              selected.symbol === h.symbol

                            return (
                              <button
                                type="button"
                                key={`${h.accountId}-${h.symbol}-${idx}`}
                                onClick={(e) => {
                                  e.stopPropagation()
                                  setSelected(
                                    isSelectedHolding
                                      ? { type: 'account', accountId, accountName: h.accountName }
                                      : {
                                          type: 'holding',
                                          accountId: h.accountId,
                                          accountName: h.accountName,
                                          symbol: h.symbol,
                                        },
                                  )
                                }}
                                className={`w-full text-left px-5 py-3 rounded-2xl flex items-center justify-between text-xs font-medium transition-all ${
                                  isSelectedHolding
                                    ? 'bg-primary/10 text-primary border border-primary/20'
                                    : 'hover:bg-zinc-800 text-zinc-400 border border-transparent'
                                }`}
                              >
                                <span className="font-bold">{h.symbol}</span>
                                <span>{formatCurrency(h.valueCents)}</span>
                              </button>
                            )
                          })}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Performance for selected item: Last 30 days + Past 12 months */}
        {selected !== null && selectedLabel && (
          <div className="lg:col-span-12 bg-card border border-border rounded-4xl p-8 shadow-2xl">
            <div className="flex justify-between items-start mb-10">
              <div>
                <h2 className="text-2xl font-bold text-white mb-1">Performance</h2>
                <p className="text-zinc-400 font-medium flex items-center gap-2">
                  <span className="w-2 h-2 rounded-full bg-primary" />
                  {selectedLabel}
                </p>
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-10">
              {/* Last 30 days (daily) */}
              <div className="flex flex-col h-[400px]">
                <h3 className="text-xs font-bold text-zinc-500 uppercase tracking-widest mb-6">
                  Last 30 days (daily)
                </h3>
                <div className="flex-1 min-h-0">
                  {selected.type === 'total' && (
                    <>
                      {totalDailySeries.length === 0 && !snapshotsLoading ? (
                        <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl">
                          <p className="text-sm text-zinc-600 font-medium">
                            No daily snapshot data yet.
                          </p>
                        </div>
                      ) : (
                        <TimeSeriesChart title="" data={totalDailyChartData} />
                      )}
                    </>
                  )}
                  {selected.type === 'account' && (
                    <>
                      {historyLoading ? (
                        <div className="h-full bg-zinc-800/50 animate-pulse rounded-3xl" />
                      ) : accountDailySeries.length === 0 ? (
                        <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl">
                          <p className="text-sm text-zinc-600 font-medium">
                            No daily history for this account yet.
                          </p>
                        </div>
                      ) : (
                        <TimeSeriesChart
                          title=""
                          data={accountDailySeries.map((s) => ({
                            date: s.date,
                            value: s.valueCents / 100,
                          }))}
                        />
                      )}
                    </>
                  )}
                  {selected.type === 'holding' && (
                    <>
                      {historyLoading ? (
                        <div className="h-full bg-zinc-800/50 animate-pulse rounded-3xl" />
                      ) : holdingDailySeries.length === 0 ? (
                        <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl">
                          <p className="text-sm text-zinc-600 font-medium">
                            No daily history for this holding yet.
                          </p>
                        </div>
                      ) : (
                        <TimeSeriesChart
                          title=""
                          data={holdingDailySeries.map((s) => ({
                            date: s.date,
                            value: s.valueCents / 100,
                          }))}
                        />
                      )}
                    </>
                  )}
                </div>
              </div>

              {/* Past 12 months (monthly) */}
              <div className="flex flex-col h-[400px]">
                <h3 className="text-xs font-bold text-zinc-500 uppercase tracking-widest mb-6">
                  Past 12 months (monthly)
                </h3>
                <div className="flex-1 min-h-0">
                  {selected.type === 'total' && (
                    <>
                      {totalMonthlySeries.length === 0 && !snapshotsLoading ? (
                        <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl">
                          <p className="text-sm text-zinc-600 font-medium">
                            No monthly snapshot data yet.
                          </p>
                        </div>
                      ) : (
                        <TimeSeriesChart title="" data={totalMonthlyChartData} isMonthly={true} />
                      )}
                    </>
                  )}
                  {selected.type === 'account' && (
                    <>
                      {accountMonthlyLoading ? (
                        <div className="h-full bg-zinc-800/50 animate-pulse rounded-3xl" />
                      ) : accountMonthly.length === 0 ? (
                        <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl">
                          <p className="text-sm text-zinc-600 font-medium">
                            No monthly history for this account yet.
                          </p>
                        </div>
                      ) : (
                        <TimeSeriesChart
                          title=""
                          data={accountMonthly.slice(-12).map((s) => ({
                            date: s.date,
                            value: s.portfolioValueCents / 100,
                          }))}
                          isMonthly={true}
                        />
                      )}
                    </>
                  )}
                  {selected.type === 'holding' && (
                    <div className="h-full flex items-center justify-center border border-dashed border-border rounded-3xl p-10 text-center">
                      <p className="text-xs text-zinc-500 font-medium">
                        Monthly performance per holding is not stored; only total portfolio and
                        per-account monthly history are available.
                      </p>
                    </div>
                  )}
                </div>
              </div>
            </div>
          </div>
        )}

        {/* Yearly portfolio summary by account (end-of-year value) */}
        <div className="lg:col-span-12 bg-card border border-border rounded-4xl p-8 shadow-2xl">
          <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-4 mb-8">
            <div>
              <h2 className="text-xl font-bold text-white mb-1">Yearly summary</h2>
              <p className="text-zinc-500 text-sm font-medium">
                End-of-year portfolio value per account.
              </p>
            </div>
            <div className="flex items-center gap-3">
              <label className="text-xs font-bold text-zinc-500 uppercase tracking-widest">
                Year
              </label>
              <select
                value={yearlySummaryYear}
                onChange={(e) => setYearlySummaryYear(e.target.value)}
                className="bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold focus:border-primary focus:outline-none transition-all cursor-pointer"
              >
                <option value="">Select…</option>
                {Array.from(
                  { length: Math.max(0, new Date().getFullYear() - START_YEAR + 1) },
                  (_, i) => new Date().getFullYear() - i,
                ).map((y) => (
                  <option key={y} value={y}>
                    {y}
                  </option>
                ))}
              </select>
            </div>
          </div>

          {yearlySummaryError && (
            <p className="text-sm text-red-400 mb-6 font-medium">{yearlySummaryError}</p>
          )}
          {!yearlySummaryYear ? (
            <div className="p-10 text-center border border-dashed border-border rounded-3xl">
              <p className="text-zinc-600 font-medium">Select a year to view yearly totals.</p>
            </div>
          ) : yearlySummaryLoading ? (
            <div className="space-y-4">
              <div className="h-10 bg-zinc-800 animate-pulse rounded-xl" />
              <div className="h-10 bg-zinc-800 animate-pulse rounded-xl" />
            </div>
          ) : yearlySummary && yearlySummary.byAccount.length > 0 ? (
            <div className="bg-zinc-900 border border-border rounded-3xl overflow-hidden">
              <table className="min-w-full text-sm">
                <thead>
                  <tr className="border-b border-border bg-zinc-800/50">
                    <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                      Account
                    </th>
                    <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                      End-of-year value
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {yearlySummary.byAccount.map((row) => {
                    const displayName =
                      holdingsByAccount[row.accountId]?.accountName ?? row.accountId
                    return (
                      <tr key={row.accountId} className="hover:bg-zinc-800/30 transition-colors">
                        <td className="px-6 py-4 font-bold text-white">{displayName}</td>
                        <td className="px-6 py-4 text-right text-zinc-300 font-medium">
                          {formatCurrency(row.portfolioValueCents)}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
              <div className="px-6 py-5 bg-card border-t border-border flex justify-between items-center">
                <span className="text-sm font-bold text-white uppercase tracking-widest">
                  Total
                </span>
                <span className="text-xl font-bold text-primary">
                  {formatCurrency(
                    yearlySummary.byAccount.reduce((s, r) => s + r.portfolioValueCents, 0),
                  )}
                </span>
              </div>
            </div>
          ) : yearlySummary && yearlySummary.byAccount.length === 0 ? (
            <div className="p-10 text-center border border-dashed border-border rounded-3xl">
              <p className="text-zinc-600 font-medium">
                No yearly data for {yearlySummaryYear}. Summaries are created when retention runs.
              </p>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  )
}