import { useCallback, useEffect, useState } from 'react'
import { apiRequest } from '../lib/api'

// Lists linked Plaid accounts and supports inline rename of display names.
export function Accounts() {
  const [accounts, setAccounts] = useState<Account[]>([])
  const [netWorthCents, setNetWorthCents] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editingAccountId, setEditingAccountId] = useState<string | null>(null)
  const [editValue, setEditValue] = useState('')
  const [savingAccountId, setSavingAccountId] = useState<string | null>(null)

  // Fetches Plaid accounts from the backend.
  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await apiRequest<AccountsResponse>('/api/accounts')
      setAccounts(res.accounts ?? [])
      setNetWorthCents(res.netWorthCents ?? 0)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load accounts')
      setAccounts([])
      setNetWorthCents(0)
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Start editing a row by setting the editing state and draft text.
  const startEditing = (account: Account) => {
    setEditingAccountId(account.accountId)
    setEditValue(account.name)
    setError(null)
  }

  // Cancel editing by clearing the editing state and draft text.
  const stopEditing = () => {
    setEditingAccountId(null)
    setEditValue('')
  }

  // Save the rename, rolls back on failure.
  const saveRename = async (accountId: string) => {
    if (savingAccountId) return

    const trimmedName = editValue.trim()
    const currentAccount = accounts.find((a) => a.accountId === accountId)
    if (currentAccount && trimmedName === currentAccount.name) {
      stopEditing()
      return
    }

    setSavingAccountId(accountId)
    setError(null)

    const previousAccounts = accounts
    if (trimmedName) {
      setAccounts((prev) =>
        prev.map((a) => (a.accountId === accountId ? { ...a, name: trimmedName } : a)),
      )
    }

    try {
      const res = await apiRequest<RenameAccountResponse>('/api/accounts/rename', {
        method: 'PATCH',
        body: JSON.stringify({ accountId, displayName: trimmedName }),
      })
      setAccounts((prev) =>
        prev.map((a) => (a.accountId === accountId ? { ...a, name: res.name } : a)),
      )
      stopEditing()
    } catch (err: unknown) {
      setAccounts(previousAccounts)
      setError(err instanceof Error ? err.message : 'Failed to rename account')
    } finally {
      setSavingAccountId(null)
    }
  }

  // Format a currency amount as USD, with the minus sign before `$` when negative.
  const formatCurrency = (cents: number) => {
    const absolute = new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
    }).format(Math.abs(cents) / 100)
    return cents < 0 ? `-${absolute}` : absolute
  }

  const balanceClassName = (cents: number) =>
    cents < 0 ? 'text-red-400' : 'text-primary'

  // Format a type (e.g. "credit card") as a string.
  const formatType = (account: Account) => {
    if (account.subtype) {
      return `${account.type} · ${account.subtype}`
    }
    return account.type
  }

  return (
    <div className="max-w-6xl mx-auto py-10 px-6">
      <div className="mb-8">
        <h1 className="text-3xl font-bold text-white tracking-tight">Accounts</h1>
        <p className="text-zinc-500 mt-2 font-medium">
          Account Balances. Click a name to rename, then Save or Cancel.
        </p>
      </div>

      {error && (
        <div className="mb-6 rounded-2xl border border-red-500/20 bg-red-500/10 px-4 py-3 text-sm font-medium text-red-400">
          {error}
        </div>
      )}

      {loading && (
        <div className="bg-zinc-900 border border-border rounded-4xl p-8 animate-pulse space-y-4">
          <div className="h-4 bg-zinc-800 rounded w-1/3" />
          <div className="h-4 bg-zinc-800 rounded w-1/2" />
          <div className="h-4 bg-zinc-800 rounded w-2/5" />
        </div>
      )}

      {!loading && accounts.length === 0 && (
        <div className="p-12 text-center border border-dashed border-border rounded-4xl">
          <p className="text-zinc-600 font-medium italic">No linked accounts yet.</p>
        </div>
      )}

      {!loading && accounts.length > 0 && (
        <div className="bg-zinc-900 border border-border rounded-4xl overflow-hidden shadow-2xl">
          <table className="min-w-full text-sm">
            <thead>
              <tr className="bg-zinc-800/50 border-b border-border">
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Name
                </th>
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Institution
                </th>
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Type
                </th>
                <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                  Mask
                </th>
                <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                  Balance
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-border">
              {accounts.map((account) => {
                const isEditing = editingAccountId === account.accountId
                const isSaving = savingAccountId === account.accountId

                return (
                  <tr
                    key={account.accountId}
                    className="hover:bg-zinc-800/30 transition-colors group"
                  >
                    <td className="px-6 py-4">
                      {isEditing ? (
                        <div className="flex items-center gap-2 max-w-md">
                          <input
                            autoFocus
                            type="text"
                            value={editValue}
                            disabled={isSaving}
                            onChange={(e) => setEditValue(e.target.value)}
                            onKeyDown={(e) => {
                              if (e.key === 'Enter') {
                                e.preventDefault()
                                void saveRename(account.accountId)
                              } else if (e.key === 'Escape') {
                                e.preventDefault()
                                stopEditing()
                              }
                            }}
                            className="flex-1 min-w-0 bg-zinc-800 border border-border rounded-xl px-3 py-1.5 text-white font-bold focus:outline-none focus:ring-2 focus:ring-primary/40"
                          />
                          <button
                            type="button"
                            disabled={isSaving}
                            onClick={() => void saveRename(account.accountId)}
                            className="shrink-0 px-3 py-1.5 rounded-xl bg-primary text-background text-xs font-bold hover:bg-green-400 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            {isSaving ? 'Saving…' : 'Save'}
                          </button>
                          <button
                            type="button"
                            disabled={isSaving}
                            onClick={stopEditing}
                            className="shrink-0 px-3 py-1.5 rounded-xl border border-border text-zinc-400 text-xs font-bold hover:text-white hover:bg-zinc-800 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                          >
                            Cancel
                          </button>
                        </div>
                      ) : (
                        <button
                          type="button"
                          onClick={() => startEditing(account)}
                          className="text-left font-bold text-white group-hover:text-primary transition-colors"
                          title="Click to rename"
                        >
                          {account.name}
                        </button>
                      )}
                    </td>
                    <td className="px-6 py-4 text-zinc-400 font-medium">
                      {account.institutionName || '—'}
                    </td>
                    <td className="px-6 py-4 text-zinc-400 font-medium capitalize">
                      {formatType(account)}
                    </td>
                    <td className="px-6 py-4 text-zinc-500 font-medium">
                      {account.mask ? `••••${account.mask}` : '—'}
                    </td>
                    <td
                      className={`px-6 py-4 text-right font-bold tabular-nums ${balanceClassName(account.balanceCents)}`}
                    >
                      {formatCurrency(account.balanceCents)}
                    </td>
                  </tr>
                )
              })}
            </tbody>
            <tfoot>
              <tr className="bg-zinc-800/50 border-t border-border">
                <td
                  colSpan={4}
                  className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs"
                >
                  Total (Net Worth)
                </td>
                <td
                  className={`px-6 py-4 text-right font-bold tabular-nums ${balanceClassName(netWorthCents)}`}
                >
                  {formatCurrency(netWorthCents)}
                </td>
              </tr>
            </tfoot>
          </table>
        </div>
      )}
    </div>
  )
}

interface Account {
  provider: string
  plaidItemId?: string
  accountId: string
  name: string
  institutionName?: string
  mask?: string
  type: string
  subtype?: string
  balanceCents: number
  isLiability: boolean
}

interface AccountsResponse {
  accounts: Account[]
  netWorthCents: number
  cashCents: number
  investmentsCents: number
  liabilitiesCents: number
}

interface RenameAccountResponse {
  accountId: string
  name: string
}
