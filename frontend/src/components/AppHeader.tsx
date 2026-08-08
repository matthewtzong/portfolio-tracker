import { useEffect, useState } from 'react'
import { Link, NavLink } from 'react-router-dom'
import { supabase } from '../lib/supabase'

// CSS classes for the navigation links.
const navLinkClass =
  'py-2 px-4 text-sm font-medium rounded-full border transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-background'

const navLinkDefault =
  'text-zinc-400 bg-transparent border-transparent hover:text-zinc-100 hover:bg-white/5'

const navLinkActive = 'text-primary bg-primary/10 border-primary/20 ring-primary/30'

export function AppHeader() {
  const [user, setUser] = useState<{ email?: string } | null>(null)

  // Loads the authenticated user.
  useEffect(() => {
    supabase.auth.getUser().then(({ data: { user } }) => setUser(user))
  }, [])

  // Handles the sign out button click.
  const handleSignOut = async () => {
    await supabase.auth.signOut()
  }

  // Returns the app header.
  return (
    <header className="glass sticky top-0 z-50 border-x-0 border-t-0 border-b border-border">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex justify-between items-center h-16">
          <Link
            to="/dashboard"
            className="text-xl font-bold text-white hover:text-primary transition-colors"
          >
            My portfolio
          </Link>

          <nav className="flex items-center gap-1.5">
            <NavLink
              to="/dashboard"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Dashboard
            </NavLink>
            <NavLink
              to="/portfolio"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Portfolio
            </NavLink>
            <NavLink
              to="/accounts"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Accounts
            </NavLink>
            <NavLink
              to="/expenses"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Expenses
            </NavLink>
            <NavLink
              to="/budget"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Budget
            </NavLink>
            <NavLink
              to="/links"
              className={({ isActive: active }) =>
                `${navLinkClass} ${active ? navLinkActive : navLinkDefault}`
              }
            >
              Connections
            </NavLink>
            <div className="w-px h-6 bg-border mx-2" />
            <button
              type="button"
              onClick={handleSignOut}
              className="py-2 px-5 text-sm font-semibold text-background bg-zinc-100 rounded-full hover:bg-white transition-colors focus:outline-none focus:ring-2 focus:ring-zinc-400 focus:ring-offset-2 focus:ring-offset-background"
            >
              Sign out
            </button>
          </nav>
        </div>
        {user?.email && (
          <p className="text-[10px] text-zinc-500 pb-2 -mt-1 font-bold uppercase tracking-widest">
            Signed in as {user.email}
          </p>
        )}
      </div>
    </header>
  )
}
