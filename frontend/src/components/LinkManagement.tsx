import { useEffect, useState } from 'react'
import { apiRequest } from '../lib/api'
// import { openSnaptradeConnect, syncSnaptradeConnections } from '../lib/snaptrade'
import { PlaidLinkButton } from './PlaidLinkButton'
// import { SnaptradeConnectSection } from './SnaptradeConnectSection'

// Types for the Plaid item.
interface PlaidItem {
  itemId: string
  institutionName?: string
  status: string
  lastUpdated: string
}

/*
// Type for the Snaptrade connection (deprecated).
interface SnaptradeConnection {
  id: string
  brokerage: string
  status: string
  lastSynced?: string
}
*/

// Type for the links response.
interface LinksResponse {
  plaidItems: PlaidItem[]
  // snaptradeConnections?: SnaptradeConnection[]
  fidelityItems: PlaidItem[]
}

// Returns the link management page.
export function LinkManagement() {
  const [data, setData] = useState<LinksResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  // Loads the links.
  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const res = await apiRequest<LinksResponse>('/api/links')
      setData(res)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load links')
    } finally {
      setLoading(false)
    }
  }

  /*
  // Reconnects a Snaptrade connection by opening Connect portal.
  const reconnectSnaptradeConnection = async () => {
    setError(null)
    try {
      await openSnaptradeConnect(load)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to open Snaptrade Connect')
    }
  }
*/
  useEffect(() => {
    void load()
  }, [])

  // Removes a Plaid item.
  const removePlaidItem = async (itemId: string) => {
    setError(null)
    try {
      await apiRequest('/api/plaid/remove-item', {
        method: 'POST',
        body: JSON.stringify({ itemId }),
      })
      await load()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to remove Plaid item')
    }
  }

  // Reconnects a Plaid item by opening Link in update mode.
  const reconnectPlaidItem = async (itemId: string) => {
    setError(null)
    try {
      if (!window.Plaid) {
        throw new Error('Plaid Link script not loaded')
      }

      // Get reconnect link token.
      const { linkToken } = await apiRequest<{ linkToken: string }>(
        '/api/plaid/reconnect-link-token',
        {
          method: 'POST',
          body: JSON.stringify({ itemId }),
        },
      )

      // Create Plaid Link handler.
      const handler = window.Plaid.create({
        token: linkToken,
        onSuccess: async (publicToken, metadata) => {
          try {
            await apiRequest('/api/plaid/exchange-token', {
              method: 'POST',
              body: JSON.stringify({
                publicToken,
                institutionName: metadata.institution?.name,
                institutionId: metadata.institution?.institution_id,
              }),
            })
            await load()
          } catch (err) {
            console.error('Failed to reconnect Plaid item', err)
            setError('Failed to reconnect Plaid item')
          }
        },
        onExit: (err) => {
          if (err) {
            console.error('Plaid Link exited with error', err)
          }
        },
      })

      handler.open()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to reconnect Plaid item')
    }
  }

  /*
  // Removes a Snaptrade connection.
  const removeSnaptradeConnection = async (connectionId: string) => {
    setError(null)
    try {
      await apiRequest('/api/snaptrade/remove-connection', {
        method: 'POST',
        body: JSON.stringify({ connectionId }),
      })
      await syncSnaptradeConnections()
      await load()
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to remove Snaptrade connection')
    }
  }
*/
  // Returns the link management page.
  return (
    <div className="max-w-6xl mx-auto py-10 px-6">
      <div className="mb-10">
        <h1 className="text-3xl font-bold text-white tracking-tight mb-2">Connections</h1>
        <p className="text-zinc-500 font-medium max-w-2xl">
          Manage your bank and investment connections via Plaid.
        </p>
      </div>

      {loading && <div className="h-20 bg-zinc-800 animate-pulse rounded-3xl mb-8" />}

      {error && (
        <div className="p-4 bg-red-500/10 border border-red-500/20 rounded-2xl text-red-400 text-sm font-medium mb-8">
          {error}
        </div>
      )}

      {/* Add new connections cards */}
      <div className="grid gap-8 md:grid-cols-2 lg:grid-cols-3 mb-12">
      <div className="p-8 bg-zinc-900 border border-border rounded-4xl group hover:border-purple-500/30 transition-all shadow-xl relative overflow-hidden text-center md:col-span-2 lg:col-span-1">
          <div className="absolute -right-4 -top-4 w-24 h-24 bg-blue-500/5 rounded-full blur-2xl group-hover:bg-blue-500/10 transition-all" />
          <div className="relative">
            <h2 className="text-xl font-bold text-white mb-2">Banking & Credit</h2>
            <p className="text-zinc-500 text-sm font-medium mb-8 leading-relaxed">
              Connect checking, savings, and credit card accounts for budget tracking.
            </p>
            <PlaidLinkButton onLinked={load} products={['transactions']} />
          </div>
        </div>

        <div className="p-8 bg-zinc-900 border border-border rounded-4xl group hover:border-purple-500/30 transition-all shadow-xl relative overflow-hidden text-center md:col-span-2 lg:col-span-1">
          <div className="absolute -right-4 -top-4 w-24 h-24 bg-emerald-500/5 rounded-full blur-2xl group-hover:bg-emerald-500/10 transition-all" />
          <div className="relative">
            <h2 className="text-xl font-bold text-white mb-2">Investments</h2>
            <p className="text-zinc-500 text-sm font-medium mb-8 leading-relaxed">
              Connect brokerage and retirement accounts to sync holdings and performance.
            </p>
            <PlaidLinkButton onLinked={load} products={['investments']} />
          </div>
        </div>

        <div className="p-8 bg-zinc-900 border border-border rounded-4xl group hover:border-purple-500/30 transition-all shadow-xl relative overflow-hidden text-center md:col-span-2 lg:col-span-1">
          <div className="absolute -right-4 -top-4 w-24 h-24 bg-purple-500/5 rounded-full blur-2xl group-hover:bg-purple-500/10 transition-all" />
          <div className="relative">
            <h2 className="text-xl font-bold text-white mb-2">Full Sync</h2>
            <p className="text-zinc-500 text-sm font-medium mb-8 leading-relaxed">
              Connect institutions that handle both banking and investments.
            </p>
            <PlaidLinkButton onLinked={load} products={['transactions', 'investments']} />
          </div>
        </div>
      </div>

      {!loading && data && (
        <div className="space-y-10">
          <section>
            <div className="flex items-center gap-3 mb-6">
              <h2 className="text-xl font-bold text-white">Plaid Connections</h2>
              <span className="bg-zinc-800 text-zinc-500 px-2 py-0.5 rounded-md text-[10px] font-bold border border-border">
                {data.plaidItems.length} ACTIVE
              </span>
            </div>

            {data.plaidItems.length === 0 ? (
              <div className="p-12 text-center border border-dashed border-border rounded-4xl">
                <p className="text-zinc-600 font-medium italic italic">
                  No active Plaid connections.
                </p>
              </div>
            ) : (
              <div className="bg-zinc-900 border border-border rounded-4xl overflow-hidden shadow-2xl mb-12">
                <table className="min-w-full text-sm">
                  <thead>
                    <tr className="bg-zinc-800/50 border-b border-border">
                      <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                        Institution
                      </th>
                      <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                        Status
                      </th>
                      <th className="px-6 py-4 text-left font-bold text-white uppercase tracking-wider text-xs">
                        Last Updated
                      </th>
                      <th className="px-6 py-4 text-right font-bold text-white uppercase tracking-wider text-xs">
                        Actions
                      </th>
                    </tr>
                  </thead>
                  <tbody className="divide-y divide-border">
                    {data.plaidItems.map((item) => (
                      <tr
                        key={item.itemId}
                        className="hover:bg-zinc-800/30 transition-colors group"
                      >
                        <td className="px-6 py-4">
                          <div className="font-bold text-white group-hover:text-primary transition-colors">
                            {item.institutionName || 'Plaid Institution'}
                          </div>
                          <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter mt-0.5">
                            ID: {item.itemId}
                          </div>
                        </td>
                        <td className="px-6 py-4">
                          <span
                            className={`px-3 py-1 rounded-full text-[10px] font-bold border ${
                              item.status === 'OK'
                                ? 'bg-primary/10 text-primary border-primary/20'
                                : 'bg-red-500/10 text-red-400 border-red-500/20'
                            }`}
                          >
                            {item.status === 'OK' ? 'CONNECTED' : item.status}
                          </span>
                        </td>
                        <td className="px-6 py-4 text-zinc-400 font-medium">
                          {new Date(item.lastUpdated).toLocaleDateString()} at{' '}
                          {new Date(item.lastUpdated).toLocaleTimeString([], {
                            hour: '2-digit',
                            minute: '2-digit',
                          })}
                        </td>
                        <td className="px-6 py-4 text-right">
                          <div className="flex items-center justify-end gap-3">
                            {item.status !== 'OK' && (
                              <button
                                type="button"
                                onClick={() => reconnectPlaidItem(item.itemId)}
                                className="px-4 py-1.5 bg-blue-500 text-white text-[11px] font-bold rounded-full hover:bg-blue-400 transition-all shadow-lg active:scale-95"
                              >
                                Reconnect
                              </button>
                            )}
                            <button
                              type="button"
                              onClick={() => removePlaidItem(item.itemId)}
                              className="px-4 py-1.5 bg-zinc-800 text-zinc-400 text-[11px] font-bold rounded-full border border-border hover:bg-red-500 hover:text-white hover:border-red-500 transition-all active:scale-95"
                            >
                              Disconnect
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          {/* <section>
            <div className="flex items-center gap-3 mb-6">
              <h2 className="text-xl font-bold text-white">Manual Connections</h2>
              <span className="bg-zinc-800 text-zinc-500 px-2 py-0.5 rounded-md text-[10px] font-bold border border-border">
                {data.fidelityItems.length} ACTIVE
              </span>
            </div>

            {data.fidelityItems.length === 0 ? (
              <div className="p-12 text-center border border-dashed border-border rounded-4xl">
                <p className="text-zinc-600 font-medium italic">
                  No manual Fidelity connections yet. Upload a CSV in the Portfolio view.
                </p>
              </div>
            ) : (
              <div className="bg-zinc-900 border border-border rounded-4xl overflow-hidden shadow-2xl">
                <table className="min-w-full text-sm">
                  <tbody className="divide-y divide-border">
                    {data.fidelityItems.map((item) => (
                      <tr
                        key={item.itemId}
                        className="hover:bg-zinc-800/30 transition-colors group"
                      >
                        <td className="px-6 py-4">
                          <div className="font-bold text-white">{item.institutionName}</div>
                          <div className="text-[10px] font-bold text-zinc-500 uppercase tracking-tighter mt-0.5">
                            MANUAL TRACKING
                          </div>
                        </td>
                        <td className="px-6 py-4">
                          <span className="px-3 py-1 rounded-full text-[10px] font-bold border bg-primary/10 text-primary border-primary/20">
                            CONNECTED
                          </span>
                        </td>
                        <td className="px-6 py-4 text-zinc-400 font-medium">
                          Last uploaded: {new Date(item.lastUpdated).toLocaleDateString()}
                        </td>
                        <td className="px-6 py-4 text-right">
                          <button
                            type="button"
                            onClick={() => removePlaidItem(item.itemId)}
                            className="px-4 py-1.5 bg-zinc-800 text-zinc-400 text-[11px] font-bold rounded-full border border-border hover:bg-red-500 hover:text-white hover:border-red-500 transition-all active:scale-95"
                          >
                            Reset
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section> */}
        </div>
      )}
    </div>
  )
}
