import { useCallback, useEffect, useMemo, useState } from 'react'
import { apiRequest } from '../lib/api'
import { BudgetBarChart } from './BudgetBarChart'

// Category types.
interface Category {
  id: number
  name: string
  expense: boolean
}

// Budget API response type.
interface BudgetResponse {
  month: string
  allocations: Record<string, number>
  spent: Record<string, number>
}

// Categories response type.
interface CategoriesResponse {
  categories: Category[]
}

const START_MONTH = '2026-03'
const START_YEAR = 2026

// Venmo is a review staging category, not a budget line.
function isBudgetableCategory(category: Category): boolean {
  return category.expense && category.name !== 'Venmo'
}

// Returns the current month in YYYY-MM.
function currentMonth(): string {
  const day = new Date()
  const year = day.getFullYear()
  const month = String(day.getMonth() + 1).padStart(2, '0')
  return `${year}-${month}`
}

export function BudgetTracker() {
  const [categories, setCategories] = useState<Category[]>([])
  const [month, setMonth] = useState(() => {
    const m = currentMonth()
    return m < START_MONTH ? START_MONTH : m
  })
  const [allocations, setAllocations] = useState<Record<string, number>>({})
  const [spent, setSpent] = useState<Record<string, number>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [successMessage, setSuccessMessage] = useState<string | null>(null)
  const [totalBudget, setTotalBudget] = useState<string>('')

  const budgetCategories = useMemo(
    () => categories.filter(isBudgetableCategory),
    [categories],
  )

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

  // Loads the budget for the selected month.
  const loadBudget = useCallback(async () => {
    if (!month) {
      setAllocations({})
      setSpent({})
      return
    }
    setLoading(true)
    setError(null)
    try {
      const res = await apiRequest<BudgetResponse>(`/api/budget?month=${encodeURIComponent(month)}`)
      const nextAllocations = res.allocations ?? {}
      const budgetableIds = new Set(
        categories.filter(isBudgetableCategory).map((cat) => String(cat.id)),
      )
      // Drop Venmo (and any other non-budgetable) keys from loaded allocations.
      const cleanedAllocations: Record<string, number> = {}
      for (const [key, value] of Object.entries(nextAllocations)) {
        if (budgetableIds.has(key) && typeof value === 'number') {
          cleanedAllocations[key] = value
        }
      }
      setAllocations(cleanedAllocations)

      // Map data from name to ID
      const spentByName = res.spent ?? {}
      const spentById: Record<string, number> = {}
      categories.forEach((cat) => {
        if (isBudgetableCategory(cat)) {
          const val = spentByName[cat.name] ?? 0
          spentById[String(cat.id)] = val
        }
      })
      setSpent(spentById)

      // Computes the current budget allocations.
      const totalAllocatedCents = Object.values(cleanedAllocations).reduce(
        (sum, value) => sum + (typeof value === 'number' ? value : 0),
        0,
      )
      setTotalBudget(totalAllocatedCents > 0 ? (totalAllocatedCents / 100).toString() : '')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load budget')
      setAllocations({})
      setSpent({})
    } finally {
      setLoading(false)
    }
  }, [month, categories])

  // Loads the budget when the month changes.
  useEffect(() => {
    void loadBudget()
  }, [loadBudget])

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

  // Builds data for budget vs spent chart.
  const budgetChartData = useMemo(() => {
    return budgetCategories.map((category) => {
      const key = String(category.id)
      const allocatedCents = allocations[key] ?? 0
      const spentCents = spent[key] ?? 0
      return {
        name: category.name,
        budget: allocatedCents / 100,
        spent: spentCents < 0 ? Math.abs(spentCents) / 100 : 0,
      }
    })
  }, [allocations, budgetCategories, spent])

  // Handles budget change for a category (value is in dollars).
  const handleAllocationChange = (categoryId: number, value: string) => {
    const amount = parseFloat(value)
    setAllocations((prev) => {
      const nextAllocations = { ...prev }
      const categoryName = String(categoryId)
      if (Number.isNaN(amount)) {
        nextAllocations[categoryName] = 0
      } else {
        nextAllocations[categoryName] = Math.round(amount * 100)
      }
      return nextAllocations
    })
  }

  // Saves the budget allocations.
  const handleSaveBudget = async () => {
    setSaving(true)
    setError(null)
    setSuccessMessage(null)

    // Normalize allocations so every budgetable category has an entry (at least 0).
    // Venmo is excluded — spend lands in the real category after review.
    const normalizedAllocations: Record<string, number> = {}
    budgetCategories.forEach((category) => {
      const key = String(category.id)
      const value = allocations[key]
      normalizedAllocations[key] = typeof value === 'number' && !Number.isNaN(value) ? value : 0
    })

    // Validate that allocations sum to the designated total budget.
    const parsedTotal = parseFloat(totalBudget)
    const totalAllocatedCents = Object.values(normalizedAllocations).reduce(
      (sum, value) => sum + (typeof value === 'number' ? value : 0),
      0,
    )

    if (Number.isNaN(parsedTotal) || parsedTotal <= 0) {
      setError('Enter a total monthly budget greater than 0 before saving.')
      setSaving(false)
      return
    }

    // Computes target budget.
    const targetCents = Math.round(parsedTotal * 100)
    if (totalAllocatedCents !== targetCents) {
      setError(
        `Invalid allocations: per-category totals ${formatCurrency(
          totalAllocatedCents,
        )} must equal your total budget ${formatCurrency(targetCents)}.`,
      )
      setSaving(false)
      return
    }

    try {
      await apiRequest('/api/budget', {
        method: 'PUT',
        body: JSON.stringify({ allocations: normalizedAllocations }),
      })
      setSuccessMessage('Budget saved. It applies to all months until you change it.')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to save budget')
    } finally {
      setSaving(false)
    }
  }

  // Returns the budget tracker page.
  return (
    <div className="max-w-6xl mx-auto py-10 px-6">
      <div className="flex justify-between items-center mb-10">
        <h1 className="text-3xl font-bold text-white tracking-tight">Budget Tracker</h1>
        <button
          type="button"
          onClick={handleSaveBudget}
          disabled={saving}
          className="bg-primary text-background px-8 py-3 rounded-full text-sm font-bold hover:bg-green-400 transition-all shadow-lg active:scale-95 disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {saving ? 'Saving...' : 'Save Budget'}
        </button>
      </div>

      <div className="bg-card border border-border rounded-4xl p-8 shadow-2xl mb-8">
        <div className="flex flex-wrap items-end gap-8 mb-10">
          <div className="flex-1 min-w-[240px]">
            <label className="block text-sm font-bold text-zinc-500 uppercase tracking-widest mb-3 ml-1">
              Analysis Month
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
            <p className="mt-2 text-sm text-zinc-500 font-medium ml-1">
              Budget numbers are global; the month only changes the spent column.
            </p>
          </div>
          <div className="flex-1 min-w-[240px]">
            <label className="block text-sm font-bold text-zinc-500 uppercase tracking-widest mb-3 ml-1">
              Total Monthly Allocation
            </label>
            <div className="relative">
              <span className="absolute left-5 top-1/2 -translate-y-1/2 text-zinc-500 font-bold">
                $
              </span>
              <input
                type="number"
                min={0}
                step="0.01"
                value={totalBudget}
                onChange={(e) => setTotalBudget(e.target.value)}
                placeholder="0.00"
                className="w-full bg-zinc-900 border border-border text-zinc-100 rounded-2xl pl-9 pr-5 py-3 text-sm focus:border-primary focus:outline-none focus:ring-1 focus:ring-primary transition-all"
              />
            </div>
            <p className="mt-2 text-sm text-zinc-500 font-medium ml-1">
              Per-category budgets must add up exactly to this total.
            </p>
          </div>
        </div>

        {error && (
          <div className="mb-6 p-4 bg-red-500/10 border border-red-500/20 rounded-2xl text-red-400 text-sm font-medium">
            {error}
          </div>
        )}
        {successMessage && (
          <div className="mb-6 p-4 bg-primary/10 border border-primary/20 rounded-2xl text-primary text-sm font-medium">
            {successMessage}
          </div>
        )}

        {loading ? (
          <div className="space-y-4">
            <div className="h-[300px] bg-zinc-800 animate-pulse rounded-3xl" />
            <div className="h-48 bg-zinc-800 animate-pulse rounded-3xl" />
          </div>
        ) : (
          <>
            <div className="mb-12 bg-zinc-900 border border-border p-8 rounded-4xl">
              <BudgetBarChart
                title={`Monthly Budget Utilization (${month})`}
                data={budgetChartData}
              />
            </div>

            <div className="bg-zinc-900 border border-border rounded-3xl overflow-hidden shadow-xl mb-8">
              <table className="min-w-full text-sm">
                <thead>
                  <tr className="bg-zinc-800/50 border-b border-border">
                    <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-sm">
                      Category
                    </th>
                    <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-sm">
                      Allocated
                    </th>
                    <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-sm">
                      Spent
                    </th>
                    <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-sm">
                      Remaining
                    </th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {budgetCategories.length === 0 ? (
                    <tr>
                      <td
                        colSpan={4}
                        className="px-6 py-12 text-center text-zinc-500 font-medium italic"
                      >
                        No expense categories available.
                      </td>
                    </tr>
                  ) : (
                    budgetCategories.map((category) => {
                        const key = String(category.id)
                        const allocatedCents = allocations[key] ?? 0
                        const spentCents = spent[key] ?? 0
                        const remainingCents = allocatedCents + spentCents

                        return (
                          <tr
                            key={category.id}
                            className="hover:bg-zinc-800/30 transition-colors group"
                          >
                            <td className="px-6 py-4 font-bold text-white text-sm">
                              {category.name}
                            </td>
                            <td className="px-6 py-4 text-right text-sm">
                              <div className="flex justify-end items-center">
                                <span className="text-zinc-500 mr-2 text-sm font-bold">$</span>
                                <input
                                  type="number"
                                  min={0}
                                  step="0.01"
                                  value={
                                    allocatedCents > 0 ? (allocatedCents / 100).toString() : ''
                                  }
                                  onChange={(e) =>
                                    handleAllocationChange(category.id, e.target.value)
                                  }
                                  className="w-24 bg-zinc-800 border border-border text-zinc-100 rounded-lg px-2 py-1 text-right text-sm focus:border-primary focus:outline-none transition-all"
                                  placeholder="0.00"
                                />
                              </div>
                            </td>
                            <td className="px-6 py-4 text-right text-sm">
                              {spentCents === 0 ? (
                                <span className="text-zinc-600 font-bold">—</span>
                              ) : (
                                <span
                                  className={`font-bold text-sm ${spentCents > 0 ? 'text-green-500' : 'text-red-500'
                                    }`}
                                >
                                  {formatCurrency(spentCents)}
                                </span>
                              )}
                            </td>
                            <td className="px-6 py-4 text-right text-sm">
                              {allocatedCents === 0 ? (
                                <span className="text-zinc-600 text-sm font-bold uppercase tracking-tighter">
                                  No budget
                                </span>
                              ) : (
                                <span
                                  className={`font-bold text-sm ${remainingCents < 0 ? 'text-red-500' : 'text-primary'
                                    }`}
                                >
                                  {formatCurrency(remainingCents)}
                                </span>
                              )}
                            </td>
                          </tr>
                        )
                      })
                  )}
                </tbody>
              </table>
            </div>

            {/* Overall totals summary cards */}
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div className="bg-zinc-900 border border-border p-6 rounded-3xl">
                <div className="flex justify-between items-center mb-3">
                  <span className="text-zinc-500 text-sm font-bold uppercase tracking-widest">
                    Total Planned
                  </span>
                  <span className="text-white font-bold">
                    {formatCurrency(
                      Object.values(allocations).reduce(
                        (sum, value) => sum + (typeof value === 'number' ? value : 0),
                        0,
                      ),
                    )}
                  </span>
                </div>
                <div className="flex justify-between items-center">
                  <span className="text-zinc-500 text-sm font-bold uppercase tracking-widest">
                    Spent in {month}
                  </span>
                  <span className="text-red-500 font-bold">
                    {formatCurrency(
                      Object.values(spent).reduce(
                        (sum, value) => sum + (typeof value === 'number' ? value : 0),
                        0,
                      ),
                    )}
                  </span>
                </div>
              </div>
              <div className="bg-zinc-900 border border-border p-6 rounded-3xl flex flex-col justify-center">
                <span className="text-zinc-500 text-sm font-bold uppercase tracking-widest mb-1 block text-right">
                  Total Remaining
                </span>
                <p
                  className={`text-3xl font-bold text-right tracking-tight ${Object.values(allocations).reduce(
                    (sum, v) => sum + (typeof v === 'number' ? v : 0),
                    0,
                  ) +
                      Object.values(spent).reduce(
                        (sum, v) => sum + (typeof v === 'number' ? v : 0),
                        0,
                      ) >=
                      0
                      ? 'text-primary'
                      : 'text-red-500'
                    }`}
                >
                  {formatCurrency(
                    Object.values(allocations).reduce(
                      (sum, value) => sum + (typeof value === 'number' ? value : 0),
                      0,
                    ) +
                    Object.values(spent).reduce(
                      (sum, value) => sum + (typeof value === 'number' ? value : 0),
                      0,
                    ),
                  )}
                </p>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
