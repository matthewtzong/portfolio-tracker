import {
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts'

// Budget data point.
interface BudgetDataPoint {
  name: string
  budget: number
  spent: number
}

// Budget bar chart.
interface BudgetBarChartProps {
  title: string
  data: BudgetDataPoint[]
  height?: number
}

// Formats a number as a currency string.
function formatCurrency(value: number): string {
  return new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: 0,
  }).format(value)
}

// Returns the budget bar chart.
export function BudgetBarChart({ title, data, height = 320 }: BudgetBarChartProps) {
  if (!data.length) {
    return <p className="text-sm text-zinc-500 font-medium italic">No budget data available.</p>
  }

  // Filters out zero data points.
  const nonZeroData = data.filter((dataPoint) => dataPoint.budget > 0 || dataPoint.spent > 0)
  if (!nonZeroData.length) {
    return (
      <p className="text-sm text-zinc-500 font-medium italic">
        All categories have zero budget and spend.
      </p>
    )
  }

  return (
    <div className="w-full">
      <h3 className="text-sm font-bold text-white uppercase tracking-widest mb-6">{title}</h3>
      <div style={{ width: '100%', minWidth: 0, minHeight: height }}>
        <ResponsiveContainer width="100%" height={height}>
          <BarChart
            data={nonZeroData}
            margin={{ top: 10, right: 20, bottom: 40, left: 0 }}
            barCategoryGap={16}
          >
            <CartesianGrid strokeDasharray="3 3" vertical={false} stroke="#27272a" />
            <XAxis
              dataKey="name"
              angle={-35}
              textAnchor="end"
              height={50}
              interval={0}
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 10, fill: '#71717a', fontWeight: 600 }}
            />
            <YAxis
              tickFormatter={formatCurrency}
              tickLine={false}
              axisLine={false}
              tick={{ fontSize: 10, fill: '#71717a', fontWeight: 600 }}
            />
            <Tooltip
              cursor={{ fill: 'rgba(255,255,255,0.05)', radius: 8 }}
              contentStyle={{
                backgroundColor: '#18181b',
                border: '1px solid #27272a',
                borderRadius: '16px',
                padding: '12px',
                boxShadow: '0 20px 25px -5px rgba(0,0,0,0.5)',
              }}
              itemStyle={{ fontSize: '12px', fontWeight: 600 }}
              labelStyle={{ color: '#fff', marginBottom: '4px', fontWeight: 700 }}
              formatter={(value: unknown, name: unknown) =>
                typeof value === 'number'
                  ? [formatCurrency(value), name === 'Budget' ? 'Budget' : 'Spent']
                  : [String(value), String(name)]
              }
            />
            <Legend
              verticalAlign="top"
              align="right"
              iconType="circle"
              wrapperStyle={{ paddingBottom: '20px', fontSize: '12px', fontWeight: 700 }}
            />
            <Bar
              dataKey="budget"
              name="Budget"
              fill="#22c55e"
              radius={[4, 4, 0, 0]}
              animationDuration={1500}
            />
            <Bar
              dataKey="spent"
              name="Spent"
              fill="#ef4444"
              radius={[4, 4, 0, 0]}
              animationDuration={1500}
            />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}
