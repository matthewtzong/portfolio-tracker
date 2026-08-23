import { useCallback, useEffect, useMemo, useState } from 'react'
import { apiRequest } from '../lib/api'
import { CategoryPieChart } from './CategoryPieChart'

// Category types.
interface Category {
  id: number
  name: string
  expense: boolean
}

// Transaction types.
interface Transaction {
  id: number
  date: string
  amountCents: number
  name: string
  merchantName?: string
  categoryId?: number
  categoryName?: string
  reviewStatus?: string
  userNote?: string
  accountType?: string
  pending: boolean
}

// Transactions response type.
interface TransactionsResponse {
  transactions: Transaction[]
}

// Monthly summary of transactions
interface TransactionsSummaryResponse {
  incomeCents: number
  expensesCents: number
  investedCents: number
  pendingReviewCount: number
}

// Categories response type.
interface CategoriesResponse {
  categories: Category[]
}

// Yearly expense summary (by category).
interface YearlyExpenseCategory {
  categoryId: number
  categoryName: string
  totalCents: number
  transactionCount: number
}

interface YearlyExpenseSummaryResponse {
  year: number
  byCategory: YearlyExpenseCategory[]
}

const START_MONTH = '2026-03'
const START_YEAR = 2026

function categoryDisplayLabel(tx: Transaction): string {
  if (tx.reviewStatus === 'pending') {
    return 'Needs review'
  }
  return tx.categoryName ?? 'Uncategorized'
}

// Returns the current month in YYYY-MM.
function currentMonth(): string {
  const day = new Date()
  const year = day.getFullYear()
  const month = String(day.getMonth() + 1).padStart(2, '0')
  return `${year}-${month}`
}

export function ExpenseTracker() {
  const [transactions, setTransactions] = useState<Transaction[]>([])
  const [categories, setCategories] = useState<Category[]>([])
  const [month, setMonth] = useState(() => {
    const m = currentMonth()
    return m < START_MONTH ? START_MONTH : m
  })
  const [categoryId, setCategoryId] = useState<string>('')
  const [reviewFilter, setReviewFilter] = useState<'all' | 'pending'>('all')
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [summary, setSummary] = useState<TransactionsSummaryResponse | null>(null)
  const [summaryLoading, setSummaryLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const [reviewingTx, setReviewingTx] = useState<Transaction | null>(null)
  const [modalMode, setModalMode] = useState<'edit' | 'notes'>('edit')
  const [reviewCategoryId, setReviewCategoryId] = useState<string>('')
  const [reviewNote, setReviewNote] = useState('')
  const [reviewSaving, setReviewSaving] = useState(false)
  const [reviewError, setReviewError] = useState<string | null>(null)

  const [yearlySummaryYear, setYearlySummaryYear] = useState<string>('')
  const [yearlySummary, setYearlySummary] = useState<YearlyExpenseSummaryResponse | null>(null)
  const [yearlySummaryLoading, setYearlySummaryLoading] = useState(false)
  const [yearlySummaryError, setYearlySummaryError] = useState<string | null>(null)

  // Loads the categories from the backend.
  const loadCategories = useCallback(async () => {
    try {
      const res = await apiRequest<CategoriesResponse>('/api/categories')
      setCategories(res.categories ?? [])
    } catch {
      setCategories([])
    }
  }, [])

  // Loads the categories on mount.
  useEffect(() => {
    void loadCategories()
  }, [loadCategories])

  // Loads the transactions from the backend.
  const loadTransactions = useCallback(async () => {
    setLoading(true)
    setError(null)
    // Creates the transaction query string.
    try {
      const params = new URLSearchParams()
      if (month) {
        params.set('month', month)
      }
      if (categoryId) {
        params.set('category', categoryId)
      }
      if (search.trim()) {
        params.set('search', search.trim())
      }
      if (reviewFilter === 'pending') {
        params.set('review', 'pending')
      }
      const query = params.toString()
      const res = await apiRequest<TransactionsResponse>(
        `/api/transactions${query ? `?${query}` : ''}`,
      )
      // Hide CC payments from UI (since already calculated as expense)
      const filtered = (res.transactions ?? []).filter(tx => tx.categoryName !== 'Credit Card Payments')
      setTransactions(filtered)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load transactions')
      setTransactions([])
    } finally {
      setLoading(false)
    }
  }, [month, categoryId, search, reviewFilter])

  // Loads the transactions on mount and when filters change.
  useEffect(() => {
    void loadTransactions()
  }, [loadTransactions])

  // Loads the monthly summary (income, expenses, invested) when month changes.
  const loadSummary = useCallback(async () => {
    if (!month) {
      setSummary(null)
      setSummaryLoading(false)
      return
    }
    setSummaryLoading(true)
    setError(null)
    try {
      const res = await apiRequest<TransactionsSummaryResponse>(
        `/api/transactions/summary?month=${encodeURIComponent(month)}`,
      )
      setSummary(res)
    } catch {
      setSummary(null)
    } finally {
      setSummaryLoading(false)
    }
  }, [month])

  // Loads the monthly summary on mount and when month changes.
  useEffect(() => {
    void loadSummary()
  }, [loadSummary])

  // Loads the yearly expense summary by category.
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
      const res = await apiRequest<YearlyExpenseSummaryResponse>(
        `/api/transactions/summary/yearly?year=${encodeURIComponent(yearlySummaryYear)}`,
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

  // Formats the currency.
  const formatCurrency = (cents: number) =>
    new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      maximumFractionDigits: 2,
    }).format(cents / 100)

  // Builds month options starting from Feb 2026 through today.
  const monthOptions: string[] = []
  const now = new Date()
  const floor = new Date(Date.UTC(START_YEAR, 2, 1))
  const twelveMonthsAgo = new Date(Date.UTC(now.getFullYear(), now.getMonth() - 11, 1))
  const startMonthDate = new Date(Math.max(floor.getTime(), twelveMonthsAgo.getTime()))
  const cursor = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1))
  while (cursor >= startMonthDate) {
    const y = cursor.getUTCFullYear()
    const m = String(cursor.getUTCMonth() + 1).padStart(2, '0')
    monthOptions.push(`${y}-${m}`)
    cursor.setUTCMonth(cursor.getUTCMonth() - 1)
  }

  // Aggregates expenses by category
  const categoryBreakdown = useMemo(() => {
    if (!transactions.length) {
      return []
    }
    const totalsByCategory: Record<string, number> = {}
    transactions.forEach((transaction) => {
      if (transaction.reviewStatus === 'pending') {
        return
      }
      const categoryName = transaction.categoryName || 'Uncategorized'
      // Exclude transfers and investments from the expense breakdown pie chart.
      if (categoryName === 'Transfer' || categoryName === 'Investments') {
        return
      }
      // Sum only the outflows (negative cents) to show a gross expense breakdown.
      // This ensures categories like Venmo show up even if they have more inflows than outflows overall.
      if (transaction.amountCents < 0) {
        const delta = Math.abs(transaction.amountCents)
        totalsByCategory[categoryName] = (totalsByCategory[categoryName] ?? 0) + delta
      }
    })
    return Object.entries(totalsByCategory).map(([name, valueCents]) => ({
      name,
      value: valueCents / 100,
    }))
  }, [transactions])

  const assignableCategories = useMemo(
    () => categories.filter((c) => c.name !== 'Venmo'),
    [categories],
  )

  const openEdit = (tx: Transaction) => {
    setModalMode('edit')
    setReviewingTx(tx)
    setReviewCategoryId(tx.categoryId != null ? String(tx.categoryId) : '')
    setReviewNote(tx.userNote ?? '')
    setReviewError(null)
  }

  const openNotes = (tx: Transaction) => {
    setModalMode('notes')
    setReviewingTx(tx)
    setReviewCategoryId(tx.categoryId != null ? String(tx.categoryId) : '')
    setReviewNote(tx.userNote ?? '')
    setReviewError(null)
  }

  const closeReview = () => {
    setReviewingTx(null)
    setReviewCategoryId('')
    setReviewNote('')
    setReviewError(null)
  }

  const handleSaveReview = async () => {
    if (!reviewingTx) {
      return
    }
    if (modalMode === 'edit' && !reviewCategoryId) {
      setReviewError('Select a category.')
      return
    }
    if (modalMode === 'notes' && reviewingTx.categoryId == null && !reviewCategoryId) {
      setReviewError('Categorize this transaction before adding a note.')
      return
    }
    const categoryId =
      modalMode === 'notes'
        ? (reviewingTx.categoryId ?? Number(reviewCategoryId))
        : Number(reviewCategoryId)
    if (!categoryId) {
      setReviewError('Select a category.')
      return
    }
    setReviewSaving(true)
    setReviewError(null)
    try {
      const body: {
        transactionId: number
        categoryId: number
        userNote?: string
      } = {
        transactionId: reviewingTx.id,
        categoryId,
      }
      if (modalMode === 'notes') {
        body.userNote = reviewNote.trim()
      }
      await apiRequest<Transaction>('/api/transactions/review', {
        method: 'PATCH',
        body: JSON.stringify(body),
      })
      closeReview()
      await loadTransactions()
      await loadSummary()
    } catch (err: unknown) {
      setReviewError(err instanceof Error ? err.message : 'Failed to save')
    } finally {
      setReviewSaving(false)
    }
  }

  // Exports transactions for the current month as CSV.
  const handleExportTransactions = async () => {
    try {
      const { supabase } = await import('../lib/supabase')
      const {
        data: { session },
      } = await supabase.auth.getSession()
      const API_URL = import.meta.env.VITE_API_URL || ''
      const response = await fetch(`${API_URL}/api/export/transactions?month=${month}`, {
        headers: {
          Authorization: `Bearer ${session?.access_token}`,
        },
      })

      if (!response.ok) {
        throw new Error('Failed to export transactions')
      }

      // Downloads the CSV file.
      const blob = await response.blob()
      const objectURL = window.URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = objectURL
      anchor.download = `transactions-${month}.csv`
      document.body.appendChild(anchor)
      anchor.click()
      window.URL.revokeObjectURL(objectURL)
      document.body.removeChild(anchor)
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to export transactions')
    }
  }

  // Returns the expense tracker page.
  return (
    <div className="max-w-6xl mx-auto py-10 px-6">
      <div className="flex justify-between items-center mb-10">
        <h1 className="text-3xl font-bold text-white tracking-tight">Expense Tracker</h1>
        <button
          onClick={handleExportTransactions}
          className="bg-primary text-background px-6 py-2.5 rounded-full text-sm font-bold hover:bg-green-400 transition-all shadow-lg active:scale-95"
        >
          Export CSV
        </button>
      </div>

      <div className="bg-card border border-border rounded-4xl p-8 shadow-2xl mb-8">
        <div className="flex flex-wrap items-end gap-6 mb-8">
          <div className="flex-1 min-w-[200px]">
            <label className="block text-xs font-bold text-zinc-500 uppercase tracking-widest mb-3 ml-1">
              Month
            </label>
            <select
              value={month}
              onChange={(e) => setMonth(e.target.value)}
              className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl px-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary transition-all cursor-pointer"
            >
              {monthOptions.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>
          <div className="flex-1 min-w-[200px]">
            <label className="block text-xs font-bold text-zinc-500 uppercase tracking-widest mb-3 ml-1">
              Category
            </label>
            <select
              value={categoryId}
              onChange={(e) => setCategoryId(e.target.value)}
              className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl px-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary transition-all cursor-pointer"
            >
              <option value="">All Categories</option>
              {categories
                .filter((c) => c.name !== 'Venmo')
                .map((c) => (
                <option key={c.id} value={String(c.id)}>
                  {c.name}
                </option>
              ))}
            </select>
          </div>
          <div className="flex-[1.5] min-w-[280px]">
            <label className="block text-xs font-bold text-zinc-500 uppercase tracking-widest mb-3 ml-1">
              Search
            </label>
            <div className="relative">
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Name or merchant..."
                className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl px-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary transition-all"
              />
            </div>
          </div>
          <div className="flex items-end">
            {(reviewFilter === 'pending' || (summary?.pendingReviewCount ?? 0) > 0) && (
              <button
                type="button"
                onClick={() =>
                  setReviewFilter((f) => (f === 'pending' ? 'all' : 'pending'))
                }
                className={`px-5 py-3 rounded-2xl text-sm font-bold border transition-all ${
                  reviewFilter === 'pending'
                    ? 'bg-zinc-800 border-border text-zinc-200 hover:border-primary/40'
                    : 'bg-orange-500/20 border-orange-500/50 text-orange-300 hover:bg-orange-500/30'
                }`}
              >
                {reviewFilter === 'pending' ? 'Done' : 'Needs review'}
              </button>
            )}
          </div>
        </div>

        {summary && summary.pendingReviewCount > 0 && reviewFilter !== 'pending' && (
          <div className="mb-6 p-4 bg-orange-500/10 border border-orange-500/20 rounded-2xl text-orange-300 text-sm font-medium flex flex-wrap items-center justify-between gap-3">
            <span>
              {summary.pendingReviewCount} Venmo transaction
              {summary.pendingReviewCount === 1 ? '' : 's'} need review this month.
            </span>
            <button
              type="button"
              onClick={() => setReviewFilter('pending')}
              className="text-xs font-bold uppercase tracking-wider text-orange-200 hover:text-white transition-colors"
            >
              Review now
            </button>
          </div>
        )}

        {error && (
          <div className="mb-6 p-4 bg-red-500/10 border border-red-500/20 rounded-2xl text-red-400 text-sm font-medium">
            {error}
          </div>
        )}

        {/* Monthly summary cards */}
        {month && (
          <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-10">
            {summaryLoading ? (
              Array.from({ length: 4 }).map((_, i) => (
                <div key={i} className="h-24 bg-zinc-800 animate-pulse rounded-3xl" />
              ))
            ) : summary ? (
              <>
                <div className="bg-zinc-900 border border-border p-6 rounded-3xl group hover:border-green-500/30 transition-all">
                  <span className="text-zinc-500 text-xs font-bold uppercase tracking-widest mb-2 block">
                    Income
                  </span>
                  <p className="text-2xl font-bold text-green-500 tracking-tight">
                    {formatCurrency(summary.incomeCents)}
                  </p>
                </div>
                <div className="bg-zinc-900 border border-border p-6 rounded-3xl group hover:border-red-500/30 transition-all">
                  <span className="text-zinc-500 text-xs font-bold uppercase tracking-widest mb-2 block">
                    Expenses
                  </span>
                  <p className="text-2xl font-bold text-red-500 tracking-tight">
                    {formatCurrency(summary.expensesCents)}
                  </p>
                </div>
                <div className="bg-zinc-900 border border-border p-6 rounded-3xl group hover:border-blue-500/30 transition-all">
                  <span className="text-zinc-500 text-xs font-bold uppercase tracking-widest mb-2 block">
                    Investments
                  </span>
                  <p className="text-2xl font-bold text-blue-500 tracking-tight">
                    {formatCurrency(summary.investedCents)}
                  </p>
                </div>
                <div className="bg-zinc-900 border border-border p-6 rounded-3xl group hover:border-primary/30 transition-all">
                  <span className="text-zinc-500 text-xs font-bold uppercase tracking-widest mb-2 block">
                    Net Savings
                  </span>
                  <p
                    className={`text-2xl font-bold tracking-tight ${summary.incomeCents - summary.expensesCents - summary.investedCents >= 0
                      ? 'text-green-500'
                      : 'text-red-500'
                      }`}
                  >
                    {formatCurrency(
                      summary.incomeCents - summary.expensesCents - summary.investedCents,
                    )}
                  </p>
                </div>
              </>
            ) : (
              <div className="col-span-4 p-6 bg-zinc-900 border border-dashed border-border rounded-3xl text-center">
                <p className="text-sm text-zinc-500 font-medium italic">
                  No summary for this month.
                </p>
              </div>
            )}
          </div>
        )}

        {month && categoryBreakdown.length > 0 && (
          <div className="mb-12 bg-zinc-900 border border-border p-8 rounded-4xl">
            <CategoryPieChart title={`Expense Breakdown (${month})`} data={categoryBreakdown} />
          </div>
        )}

        <div className="bg-zinc-900 border border-border rounded-3xl overflow-hidden shadow-xl">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="bg-zinc-800/50 border-b border-border">
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Date
                </th>
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Transaction
                </th>
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Category
                </th>
                <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                  Amount
                </th>
                <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                  Actions
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {loading && transactions.length === 0 ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i}>
                    <td colSpan={5} className="px-6 py-4">
                      <div className="h-4 bg-zinc-800 animate-pulse rounded w-full" />
                    </td>
                  </tr>
                ))
              ) : transactions.length === 0 ? (
                <tr>
                  <td colSpan={5} className="px-6 py-12 text-center">
                    <p className="text-zinc-500 font-medium italic">
                      No transactions found for this period.
                    </p>
                  </td>
                </tr>
              ) : (
                transactions.map((tx) => (
                  <tr
                    key={tx.id}
                    className="hover:bg-zinc-800/30 transition-colors cursor-default group"
                  >
                    <td className="px-6 py-4 text-zinc-400 font-medium">{tx.date}</td>
                    <td className="px-6 py-4">
                      <div className="font-bold text-white group-hover:text-primary transition-colors">
                        {tx.name}
                      </div>
                      {tx.merchantName && (
                        <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter mt-0.5">
                          {tx.merchantName}
                        </div>
                      )}
                    </td>
                    <td className="px-6 py-4">
                      <div className="flex flex-col items-start gap-1 min-w-[7rem]">
                        <span
                          className={`inline-block max-w-[11rem] rounded-lg px-2.5 py-1 text-xs font-semibold leading-snug border ${
                            tx.reviewStatus === 'pending'
                              ? 'bg-orange-500/10 text-orange-300 border-orange-500/30'
                              : 'bg-zinc-800 text-zinc-300 border-border'
                          }`}
                        >
                          {categoryDisplayLabel(tx)}
                        </span>
                        {tx.pending && (
                          <span className="text-[10px] font-medium text-orange-400/90">
                            Bank pending
                          </span>
                        )}
                      </div>
                    </td>
                    <td className="px-6 py-4 text-right font-bold text-xs">
                      {(() => {
                        const displayAmount = tx.amountCents
                        const isNegative = displayAmount < 0
                        return (
                          <span className={isNegative ? 'text-red-500' : 'text-green-500'}>
                            {formatCurrency(displayAmount)}
                          </span>
                        )
                      })()}
                    </td>
                    <td className="px-6 py-4 text-right">
                      <div className="flex items-center justify-end gap-3">
                        <button
                          type="button"
                          onClick={() => openNotes(tx)}
                          className={`text-xs font-bold uppercase tracking-wider transition-colors ${
                            tx.userNote
                              ? 'text-orange-300 hover:text-orange-200'
                              : 'text-zinc-500 hover:text-zinc-300'
                          }`}
                        >
                          Notes
                        </button>
                        {tx.reviewStatus === 'pending' ? (
                          <button
                            type="button"
                            onClick={() => openEdit(tx)}
                            className="text-xs font-bold uppercase tracking-wider text-primary hover:text-green-400 transition-colors"
                          >
                            Categorize
                          </button>
                        ) : (
                          <button
                            type="button"
                            onClick={() => openEdit(tx)}
                            className="text-xs font-bold uppercase tracking-wider text-zinc-500 hover:text-zinc-300 transition-colors"
                          >
                            Edit
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Yearly expense summary by category */}
      <div className="bg-card border border-border rounded-4xl p-8 shadow-2xl">
        <div className="flex flex-col md:flex-row justify-between items-start md:items-center gap-6 mb-8">
          <div>
            <h2 className="text-xl font-bold text-white mb-1">Yearly Summary</h2>
            <p className="text-zinc-500 text-sm font-medium">
              Total spent per category for the year.
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
              <option value="">Select Year</option>
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
          <div className="p-12 text-center border border-dashed border-border rounded-3xl">
            <p className="text-zinc-600 font-medium">Select a year to view yearly totals.</p>
          </div>
        ) : yearlySummaryLoading ? (
          <div className="space-y-4">
            <div className="h-12 bg-zinc-800 animate-pulse rounded-2xl" />
            <div className="h-12 bg-zinc-800 animate-pulse rounded-2xl" />
          </div>
        ) : yearlySummary && yearlySummary.byCategory.length > 0 ? (
          <div className="bg-zinc-900 border border-border rounded-3xl overflow-hidden shadow-xl">
            <table className="min-w-full text-sm">
              <thead className="bg-zinc-800/50 border-b border-border">
                <tr>
                  <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                    Category
                  </th>
                  <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                    Amount
                  </th>
                  <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                    Count
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border">
                {yearlySummary.byCategory.map((row) => (
                  <tr key={row.categoryId} className="hover:bg-zinc-800/30 transition-colors">
                    <td className="px-6 py-4 font-bold text-white">{row.categoryName}</td>
                    <td className="px-6 py-4 text-right text-red-500 font-bold">
                      {formatCurrency(row.totalCents)}
                    </td>
                    <td className="px-6 py-4 text-right text-zinc-400 font-medium">
                      {row.transactionCount}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="px-6 py-6 bg-card border-t border-border flex justify-between items-center">
              <span className="text-sm font-bold text-white uppercase tracking-widest">
                Annual Total
              </span>
              <span className="text-2xl font-bold text-red-500">
                {formatCurrency(yearlySummary.byCategory.reduce((s, r) => s + r.totalCents, 0))}
              </span>
            </div>
          </div>
        ) : yearlySummary && yearlySummary.byCategory.length === 0 ? (
          <div className="p-12 text-center border border-dashed border-border rounded-3xl">
            <p className="text-zinc-600 font-medium">No yearly data for {yearlySummaryYear}.</p>
          </div>
        ) : null}
      </div>

      {reviewingTx && (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/70">
          <div className="w-full max-w-md bg-card border border-border rounded-4xl p-8 shadow-2xl">
            <h3 className="text-xl font-bold text-white mb-1">
              {modalMode === 'notes' ? 'Notes' : reviewingTx.reviewStatus === 'pending' ? 'Categorize transaction' : 'Edit category'}
            </h3>
            <p className="text-sm text-zinc-500 mb-6 truncate">{reviewingTx.name}</p>

            {modalMode === 'edit' ? (
              <div>
                <label className="block text-xs font-bold text-zinc-500 uppercase tracking-widest mb-2">
                  Category
                </label>
                <select
                  value={reviewCategoryId}
                  onChange={(e) => setReviewCategoryId(e.target.value)}
                  className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl px-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary"
                >
                  <option value="">Select category</option>
                  {assignableCategories.map((c) => (
                    <option key={c.id} value={String(c.id)}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </div>
            ) : (
              <div>
                <label className="block text-xs font-bold text-zinc-500 uppercase tracking-widest mb-2">
                  Note
                </label>
                <textarea
                  value={reviewNote}
                  onChange={(e) => setReviewNote(e.target.value)}
                  rows={6}
                  placeholder="e.g. March utilities — reimbursed roommate"
                  className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl px-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary resize-y max-h-48 overflow-y-auto"
                />
              </div>
            )}

            {reviewError && (
              <p className="mt-4 text-sm text-red-400 font-medium">{reviewError}</p>
            )}

            <div className="mt-8 flex justify-end gap-3">
              <button
                type="button"
                onClick={closeReview}
                disabled={reviewSaving}
                className="px-5 py-2.5 rounded-full text-sm font-bold text-zinc-400 hover:text-white transition-colors"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void handleSaveReview()}
                disabled={reviewSaving}
                className="bg-primary text-background px-6 py-2.5 rounded-full text-sm font-bold hover:bg-green-400 transition-all disabled:opacity-50"
              >
                {reviewSaving ? 'Saving...' : 'Save'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
