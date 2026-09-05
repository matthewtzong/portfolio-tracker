import { useCallback, useEffect, useState } from 'react'
import { apiRequest } from '../lib/api'
import { CategoryPieChart } from './CategoryPieChart'

interface OverviewAccount {
  accountId: string
  accountName: string
  quantity: number
  valueCents: number
}

interface OverviewSymbol {
  symbol: string
  assetClass: string
  quantity: number
  valueCents: number
  weightBps: number
  accounts: OverviewAccount[]
}

interface OverviewBucket {
  key: string
  label: string
  valueCents: number
  weightBps: number
  targetBps?: number
}

interface OverviewDelta {
  absoluteCents: number
  percentBps?: number
  fromDate?: string
  toDate?: string
}

interface OverviewMover {
  symbol: string
  absoluteCents: number
  percentBps?: number
  fromCents: number
  toCents: number
}

interface OverviewWarning {
  type: string
  severity: string
  message: string
  symbols?: string[]
  key?: string
}

interface OverviewMoversPeriod {
  fromDate?: string
  toDate?: string
  gainers: OverviewMover[]
  losers: OverviewMover[]
}

interface PortfolioOverview {
  totalValueCents: number
  dayOverDay?: OverviewDelta
  monthOverMonth?: OverviewDelta
  bySymbol: OverviewSymbol[]
  byBucket: OverviewBucket[]
  byAssetClass: OverviewBucket[]
  moversDay: OverviewMoversPeriod
  moversWeek: OverviewMoversPeriod
  warnings: OverviewWarning[]
  targetsSumBps: number
  targetsComplete: boolean
  driftWarnBps: number
  singleStockMaxBps: number
}

interface AllocationTarget {
  kind: 'ticker' | 'asset_class' | 'group'
  key: string
  targetBps: number
  members?: string[]
}

interface TargetsResponse {
  targets: AllocationTarget[]
  driftWarnBps: number
  singleStockMaxBps: number
  targetsSumBps: number
  targetsComplete: boolean
}

function formatCurrency(cents: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
  }).format(cents / 100)
}

function formatBpsAsPercent(bps: number): string {
  return `${(bps / 100).toFixed(1)}%`
}

function formatSnapshotDate(date: string): string {
  const parsed = new Date(`${date}T12:00:00`)
  if (Number.isNaN(parsed.getTime())) return date
  return parsed.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
}

function formatMoversPeriodLabel(from?: string, to?: string, kind: 'day' | 'week' = 'day'): string {
  if (!from || !to) {
    if (kind === 'week') {
      return 'Need at least 5 days of nightly snapshots for weekly movers. Ranked by % change; new positions excluded.'
    }
    return 'Need at least two nightly snapshots. Ranked by % change; new positions excluded.'
  }
  const window =
    kind === 'week'
      ? 'Last Week'
      : 'Last Day'
  return `${window}: ${formatSnapshotDate(from)} → ${formatSnapshotDate(to)}.`
}

function formatDeltaPeriod(delta?: OverviewDelta): string | null {
  if (!delta?.fromDate || !delta?.toDate) return null
  return `${formatSnapshotDate(delta.fromDate)} → ${formatSnapshotDate(delta.toDate)}`
}

function formatDelta(delta?: OverviewDelta): string {
  if (!delta) return '—'
  const sign = delta.absoluteCents >= 0 ? '+' : ''
  const pct =
    delta.percentBps != null ? ` (${sign}${(delta.percentBps / 100).toFixed(2)}%)` : ''
  return `${sign}${formatCurrency(delta.absoluteCents)}${pct}`
}

function assetClassLabel(key: string): string {
  switch (key) {
    case 'etf':
      return 'Other ETFs'
    case 'stock':
      return 'Single stocks'
    case 'other':
      return 'Other'
    default:
      return key
  }
}

export function Allocations() {
  const [overview, setOverview] = useState<PortfolioOverview | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [targets, setTargets] = useState<AllocationTarget[]>([])
  const [driftWarnBps, setDriftWarnBps] = useState(500)
  const [singleStockMaxBps, setSingleStockMaxBps] = useState(1000)
  const [saving, setSaving] = useState(false)
  const [saveMessage, setSaveMessage] = useState<string | null>(null)

  const [newKind, setNewKind] = useState<'ticker' | 'asset_class' | 'group'>('ticker')
  const [newKey, setNewKey] = useState('')
  const [newMembers, setNewMembers] = useState('')
  const [newPercent, setNewPercent] = useState('')

  const loadOverview = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await apiRequest<PortfolioOverview>('/api/portfolio/overview')
      setOverview(data)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load allocations')
    } finally {
      setLoading(false)
    }
  }, [])

  const loadTargets = useCallback(async () => {
    try {
      const data = await apiRequest<TargetsResponse>('/api/portfolio/targets')
      setTargets(data.targets ?? [])
      setDriftWarnBps(data.driftWarnBps)
      setSingleStockMaxBps(data.singleStockMaxBps)
    } catch {
      // Overview still works without targets.
    }
  }, [])

  useEffect(() => {
    void loadOverview()
    void loadTargets()
  }, [loadOverview, loadTargets])

  const saveTargets = async (next: AllocationTarget[]) => {
    setSaving(true)
    setSaveMessage(null)
    try {
      const data = await apiRequest<TargetsResponse>('/api/portfolio/targets', {
        method: 'PUT',
        body: JSON.stringify({
          targets: next,
          driftWarnBps,
          singleStockMaxBps,
        }),
      })
      setTargets(data.targets ?? [])
      setSaveMessage('Targets saved')
      await loadOverview()
    } catch (err) {
      setSaveMessage(err instanceof Error ? err.message : 'Failed to save targets')
    } finally {
      setSaving(false)
    }
  }

  const handleAddTarget = () => {
    const pct = Number(newPercent)
    if (Number.isNaN(pct) || pct < 0 || pct > 100) {
      setSaveMessage('Enter a valid percent (0–100)')
      return
    }
    const targetBps = Math.round(pct * 100)

    if (newKind === 'group') {
      const key = newKey.trim()
      const members = newMembers
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
        .map((s) => s.toUpperCase())
      const uniqueMembers = [...new Set(members)]
      if (!key || uniqueMembers.length < 2) {
        setSaveMessage('Group needs a name and at least 2 tickers (comma-separated)')
        return
      }
      const withoutDup = targets.filter((t) => !(t.kind === 'group' && t.key === key))
      void saveTargets([
        ...withoutDup,
        { kind: 'group', key, targetBps, members: uniqueMembers },
      ])
      setNewKey('')
      setNewMembers('')
      setNewPercent('')
      return
    }

    const key = newKind === 'ticker' ? newKey.trim().toUpperCase() : newKey.trim().toLowerCase()
    if (!key) {
      setSaveMessage('Enter a valid key and percent (0–100)')
      return
    }
    const withoutDup = targets.filter((t) => !(t.kind === newKind && t.key === key))
    void saveTargets([...withoutDup, { kind: newKind, key, targetBps }])
    setNewKey('')
    setNewPercent('')
  }

  const handleRemoveTarget = (kind: string, key: string) => {
    void saveTargets(targets.filter((t) => !(t.kind === kind && t.key === key)))
  }

  const pieData =
    overview?.byBucket.map((b) => ({
      name: b.label,
      value: b.valueCents / 100,
    })) ?? []

  const targetsSumPercent = targets.reduce((sum, t) => sum + t.targetBps, 0) / 100

  return (
    <div className="max-w-6xl mx-auto py-10 px-4 sm:px-6 space-y-8">
      <div className="mb-2">
        <h1 className="text-3xl font-bold text-white tracking-tight mb-2">Allocations</h1>
        <p className="text-zinc-500 text-sm font-medium">
          Invested assets only (cash sweep excluded). Same ticker across accounts is combined.
        </p>
      </div>

      {error && (
        <p className="text-sm text-red-400 font-medium bg-red-500/10 border border-red-500/20 rounded-2xl px-4 py-3">
          {error}
        </p>
      )}

      {/* Totals + deltas */}
      <div className="bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl">
        {loading && !overview ? (
          <div className="h-24 bg-zinc-800 animate-pulse rounded-2xl" />
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6">
            <div>
              <p className="text-[10px] font-bold text-zinc-500 uppercase tracking-widest mb-2">
                Invested total
              </p>
              <p className="text-3xl font-bold text-white">
                {formatCurrency(overview?.totalValueCents ?? 0)}
              </p>
            </div>
            <div>
              <p className="text-[10px] font-bold text-zinc-500 uppercase tracking-widest mb-2">
                Day over day
              </p>
              <p
                className={`text-xl font-bold ${
                  (overview?.dayOverDay?.absoluteCents ?? 0) >= 0 ? 'text-green-400' : 'text-red-400'
                }`}
              >
                {formatDelta(overview?.dayOverDay)}
              </p>
              {formatDeltaPeriod(overview?.dayOverDay) && (
                <p className="text-[11px] text-zinc-500 mt-1">{formatDeltaPeriod(overview?.dayOverDay)}</p>
              )}
            </div>
            <div>
              <p className="text-[10px] font-bold text-zinc-500 uppercase tracking-widest mb-2">
                Month over month
              </p>
              <p
                className={`text-xl font-bold ${
                  (overview?.monthOverMonth?.absoluteCents ?? 0) >= 0
                    ? 'text-green-400'
                    : 'text-red-400'
                }`}
              >
                {formatDelta(overview?.monthOverMonth)}
              </p>
              {formatDeltaPeriod(overview?.monthOverMonth) && (
                <p className="text-[11px] text-zinc-500 mt-1">
                  {formatDeltaPeriod(overview?.monthOverMonth)}
                </p>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Warnings */}
      {overview && overview.warnings.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {overview.warnings.map((w, i) => {
            const tone =
              w.severity === 'error'
                ? 'bg-red-500/10 border-red-500/30 text-red-300'
                : w.severity === 'warning'
                  ? 'bg-amber-500/10 border-amber-500/30 text-amber-300'
                  : 'bg-zinc-800 border-border text-zinc-400'
            return (
              <span
                key={`${w.type}-${i}`}
                className={`text-xs font-medium px-3 py-1.5 rounded-full border ${tone}`}
              >
                {w.message}
              </span>
            )
          })}
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-8">
        {/* Pie */}
        <div className="bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl">
          <h2 className="text-xl font-bold text-white mb-1">Target buckets</h2>
          <p className="text-zinc-500 text-sm font-medium mb-6">
            Named groups and tickers first; leftovers roll into ETF / stock / other.
          </p>
          {loading && !overview ? (
            <div className="h-64 bg-zinc-800 animate-pulse rounded-2xl" />
          ) : pieData.length === 0 ? (
            <p className="text-sm text-zinc-500">No invested holdings yet.</p>
          ) : (
            <CategoryPieChart title="" data={pieData} height={280} />
          )}
          {overview && overview.byBucket.length > 0 && (
            <ul className="mt-6 space-y-2">
              {overview.byBucket.map((b) => (
                <li
                  key={`${b.key}-${b.label}`}
                  className="flex justify-between text-sm border-b border-border/50 py-2"
                >
                  <span className="text-zinc-300 font-medium">{b.label}</span>
                  <span className="text-zinc-400">
                    {formatBpsAsPercent(b.weightBps)}
                    {b.targetBps != null ? ` / target ${formatBpsAsPercent(b.targetBps)}` : ''}
                    <span className="ml-3 text-white font-bold">{formatCurrency(b.valueCents)}</span>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>

        {/* Targets editor */}
        <div className="bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl">
          <h2 className="text-xl font-bold text-white mb-1">Targets</h2>
          <p className="text-zinc-500 text-sm font-medium mb-6">
            Sum: {targetsSumPercent.toFixed(1)}%
            {targets.length > 0 && targetsSumPercent !== 100 ? ' (should be 100%)' : ''}
          </p>

          <div className="space-y-3 mb-6">
            {targets.length === 0 ? (
              <p className="text-sm text-zinc-500 italic">No targets set yet.</p>
            ) : (
              targets.map((t) => (
                <div
                  key={`${t.kind}:${t.key}`}
                  className="flex items-center justify-between bg-zinc-900 border border-border rounded-2xl px-4 py-3"
                >
                  <div>
                    <span className="text-[10px] font-bold uppercase tracking-widest text-zinc-500 mr-2">
                      {t.kind === 'ticker' ? 'Ticker' : t.kind === 'group' ? 'Group' : 'Class'}
                    </span>
                    <span className="text-white font-bold">
                      {t.kind === 'asset_class' ? assetClassLabel(t.key) : t.key}
                    </span>
                    {t.kind === 'group' && t.members && t.members.length > 0 && (
                      <p className="text-[11px] text-zinc-500 mt-1 font-medium">
                        {t.members.join(' · ')}
                      </p>
                    )}
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="text-zinc-300 font-medium">
                      {formatBpsAsPercent(t.targetBps)}
                    </span>
                    <button
                      type="button"
                      onClick={() => handleRemoveTarget(t.kind, t.key)}
                      className="text-xs font-bold text-red-400 hover:text-red-300"
                      disabled={saving}
                    >
                      Remove
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>

          <div className="flex flex-col gap-2 mb-4">
            <div className="flex flex-col sm:flex-row gap-2">
              <select
                value={newKind}
                onChange={(e) => {
                  setNewKind(e.target.value as 'ticker' | 'asset_class' | 'group')
                  setNewKey('')
                  setNewMembers('')
                }}
                className="bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
              >
                <option value="ticker">Ticker</option>
                <option value="group">Group</option>
                <option value="asset_class">Asset class</option>
              </select>
              {newKind === 'ticker' ? (
                <input
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  placeholder="VOO"
                  className="flex-1 bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold uppercase"
                />
              ) : newKind === 'group' ? (
                <input
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  placeholder="S&P 500"
                  className="flex-1 bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
                />
              ) : (
                <select
                  value={newKey}
                  onChange={(e) => setNewKey(e.target.value)}
                  className="flex-1 bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
                >
                  <option value="">Select class</option>
                  <option value="etf">Other ETFs</option>
                  <option value="stock">Single stocks</option>
                  <option value="other">Other</option>
                </select>
              )}
              <input
                value={newPercent}
                onChange={(e) => setNewPercent(e.target.value)}
                placeholder="%"
                type="number"
                min={0}
                max={100}
                step={0.1}
                className="w-24 bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
              />
              <button
                type="button"
                onClick={handleAddTarget}
                disabled={saving}
                className="px-5 py-2 text-xs font-bold rounded-full bg-primary/20 text-primary border border-primary/30 hover:bg-primary/30 disabled:opacity-50"
              >
                Add
              </button>
            </div>
            {newKind === 'group' && (
              <input
                value={newMembers}
                onChange={(e) => setNewMembers(e.target.value)}
                placeholder="Tickers: VOO, FXAIX, SPY"
                className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold uppercase"
              />
            )}
          </div>

          <div className="grid grid-cols-2 gap-3 mb-4">
            <label className="text-xs text-zinc-500">
              Drift warn (pp)
              <input
                type="number"
                value={driftWarnBps / 100}
                onChange={(e) => setDriftWarnBps(Math.round(Number(e.target.value) * 100))}
                className="mt-1 w-full bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
              />
            </label>
            <label className="text-xs text-zinc-500">
              Single-stock cap (%)
              <input
                type="number"
                value={singleStockMaxBps / 100}
                onChange={(e) => setSingleStockMaxBps(Math.round(Number(e.target.value) * 100))}
                className="mt-1 w-full bg-zinc-900 border border-border text-zinc-100 rounded-full px-4 py-2 text-xs font-bold"
              />
            </label>
          </div>
          <button
            type="button"
            disabled={saving}
            onClick={() => void saveTargets(targets)}
            className="px-5 py-2 text-xs font-bold rounded-full bg-zinc-100 text-background hover:bg-white disabled:opacity-50"
          >
            Save settings
          </button>
          {saveMessage && <p className="mt-3 text-xs text-zinc-400 font-medium">{saveMessage}</p>}
        </div>
      </div>

      {/* Holdings table */}
      <div className="bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl">
        <h2 className="text-xl font-bold text-white mb-1">Holdings by symbol</h2>
        <p className="text-zinc-500 text-sm font-medium mb-6">Combined across accounts</p>
        <div className="bg-zinc-900 border border-border rounded-3xl overflow-x-auto">
          <table className="w-full min-w-[520px] text-left">
            <thead>
              <tr className="border-b border-border text-[10px] font-bold uppercase tracking-widest text-zinc-500">
                <th className="px-6 py-4">Symbol</th>
                <th className="px-6 py-4">Class</th>
                <th className="px-6 py-4 text-right">Qty</th>
                <th className="px-6 py-4 text-right">Weight</th>
                <th className="px-6 py-4 text-right">Value</th>
              </tr>
            </thead>
            <tbody>
              {loading && !overview ? (
                <tr>
                  <td colSpan={5} className="px-6 py-8">
                    <div className="h-8 bg-zinc-800 animate-pulse rounded-xl" />
                  </td>
                </tr>
              ) : !overview?.bySymbol.length ? (
                <tr>
                  <td colSpan={5} className="px-6 py-12 text-center text-zinc-500 italic">
                    No invested holdings.
                  </td>
                </tr>
              ) : (
                overview.bySymbol.map((row) => (
                  <tr key={row.symbol} className="border-b border-border/40 hover:bg-zinc-800/30">
                    <td className="px-6 py-4 font-bold text-white">{row.symbol}</td>
                    <td className="px-6 py-4 text-zinc-400 text-sm capitalize">{row.assetClass}</td>
                    <td className="px-6 py-4 text-right text-zinc-300 text-sm">
                      {row.quantity.toLocaleString(undefined, { maximumFractionDigits: 4 })}
                    </td>
                    <td className="px-6 py-4 text-right text-zinc-300 text-sm">
                      {formatBpsAsPercent(row.weightBps)}
                    </td>
                    <td className="px-6 py-4 text-right font-bold text-white">
                      {formatCurrency(row.valueCents)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Movers */}
      <div className="space-y-8">
        <div className="space-y-3">
          <h2 className="text-xl font-bold text-white px-1">Top Movers — Last Day</h2>
          <p className="text-zinc-500 text-sm font-medium px-1">
            {formatMoversPeriodLabel(
              overview?.moversDay?.fromDate,
              overview?.moversDay?.toDate,
              'day',
            )}
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
            <MoversCard title="Top gainers" movers={overview?.moversDay?.gainers ?? []} positive />
            <MoversCard title="Top losers" movers={overview?.moversDay?.losers ?? []} positive={false} />
          </div>
        </div>

        <div className="space-y-3">
          <h2 className="text-xl font-bold text-white px-1">Top Movers — Last Week</h2>
          <p className="text-zinc-500 text-sm font-medium px-1">
            {formatMoversPeriodLabel(
              overview?.moversWeek?.fromDate,
              overview?.moversWeek?.toDate,
              'week',
            )}
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-8">
            <MoversCard title="Top gainers" movers={overview?.moversWeek?.gainers ?? []} positive />
            <MoversCard title="Top losers" movers={overview?.moversWeek?.losers ?? []} positive={false} />
          </div>
        </div>
      </div>
    </div>
  )
}

function MoversCard({
  title,
  movers,
  positive,
}: {
  title: string
  movers: OverviewMover[]
  positive: boolean
}) {
  return (
    <div className="bg-card border border-border rounded-4xl p-6 sm:p-8 shadow-2xl">
      <h2 className="text-xl font-bold text-white mb-6">{title}</h2>
      {movers.length === 0 ? (
        <p className="text-sm text-zinc-500 italic">Not enough history yet.</p>
      ) : (
        <ul className="space-y-3">
          {movers.map((m) => (
            <li
              key={m.symbol}
              className="grid grid-cols-[1fr_5.5rem_0.5rem_6.5rem] items-center border-b border-border/50 pb-3"
            >
              <span className="font-bold text-white truncate">{m.symbol}</span>
              <span
                className={`font-bold text-sm text-left tabular-nums ${positive ? 'text-green-400' : 'text-red-400'}`}
              >
                {m.percentBps != null
                  ? `${m.percentBps >= 0 ? '+' : ''}${(m.percentBps / 100).toFixed(2)}%`
                  : '—'}
              </span>
              <span aria-hidden="true" />
              <span
                className={`font-bold text-sm text-left tabular-nums ${positive ? 'text-green-400' : 'text-red-400'}`}
              >
                {m.absoluteCents >= 0 ? '+' : ''}
                {formatCurrency(m.absoluteCents)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
