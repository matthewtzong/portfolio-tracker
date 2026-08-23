import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { Auth } from './components/Auth'
import { Dashboard } from './components/Dashboard'
import { ExpenseTracker } from './components/ExpenseTracker'
import { ProtectedRoute } from './components/ProtectedRoute'
import { LinkManagement } from './components/LinkManagement'
import { BudgetTracker } from './components/BudgetTracker'
import { Portfolio } from './components/Portfolio'
import { Allocations } from './components/Allocations'
import { Accounts } from './components/Accounts'
import { MainLayout } from './components/MainLayout'

/**
Route graph:
 - `/auth`: sign in / create account
 - `/dashboard`: protected main app shell
 - `/`: redirects to `/dashboard` (which will bounce unauthenticated users to `/auth`)
 - `/links`: protected link management page
 - `/accounts`: protected Plaid accounts list (balances + rename)
 - `/expenses`: protected expense tracker (transactions by month/category)
 - `/budget`: protected budget tracker (global budgets, monthly spent vs budget)
 - `/portfolio`: protected portfolio view (holdings, daily/monthly snapshots)
 - `/allocations`: protected allocations (weights, targets, movers)
*/
function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/auth" element={<Auth />} />
        <Route
          element={
            <ProtectedRoute>
              <MainLayout />
            </ProtectedRoute>
          }
        >
          <Route index element={<Navigate to="/dashboard" replace />} />
          <Route path="dashboard" element={<Dashboard />} />
          <Route path="accounts" element={<Accounts />} />
          <Route path="links" element={<LinkManagement />} />
          <Route path="expenses" element={<ExpenseTracker />} />
          <Route path="budget" element={<BudgetTracker />} />
          <Route path="portfolio" element={<Portfolio />} />
          <Route path="allocations" element={<Allocations />} />
        </Route>
      </Routes>
    </BrowserRouter>
  )
}

export default App
