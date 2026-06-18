import { useMemo } from 'react'
import { useWebSocket } from './hooks/useWebSocket.js'
import WarehouseCanvas from './components/WarehouseCanvas.jsx'

export default function App() {
  const { connected, map, agvs, orders, agvPositionsRef } = useWebSocket('ws://localhost:8080/ws')

  const stats = useMemo(() => {
    let moving = 0
    let idle = 0
    let withCargo = 0
    let charging = 0
    let lowBattery = 0
    let returning = 0
    let avgSOC = 0
    let avgTemp = 0
    const list = Object.values(agvs)
    list.forEach(a => {
      if (a.status === 1) moving++
      else if (a.status === 0 || a.status === undefined) idle++
      if (a.status === 4 || a.isCharging) charging++
      if (a.hasCargo) withCargo++
      if (a.lowBattery || a.lowbat || (a.battery != null && a.battery < 30)) lowBattery++
      if (a.isReturning) returning++
      if (a.battery != null) avgSOC += a.battery
      if (a.temperature != null) avgTemp += a.temperature
    })
    const n = list.length || 1
    return {
      moving, idle, withCargo, total: list.length,
      charging, lowBattery, returning,
      avgSOC: Math.round(avgSOC / n),
      avgTemp: (avgTemp / n).toFixed(1)
    }
  }, [agvs])

  const orderStats = useMemo(() => {
    let pending = 0
    let progress = 0
    let done = 0
    Object.values(orders).forEach(o => {
      if (o.status === 0 || o.status === 1) pending++
      else if (o.status === 2) progress++
      else if (o.status === 3) done++
    })
    return { pending, progress, done, total: Object.keys(orders).length }
  }, [orders])

  const statusLabel = (a) => {
    if (a.isCharging) return <span className="agv-status status-charging">充电中</span>
    if (a.isReturning) return <span className="agv-status status-returning">回充中</span>
    if (a.status === 8 || a.lowBattery || a.lowbat) return <span className="agv-status status-lowbat">低电量</span>
    switch (a.status) {
      case 0: return <span className="agv-status status-idle">空闲</span>
      case 1: return <span className="agv-status status-moving">移动</span>
      case 2: return <span className="agv-status status-picking">取货</span>
      case 3: return <span className="agv-status status-placing">放货</span>
      case 4: return <span className="agv-status status-charging">充电</span>
      case 5: return <span className="agv-status status-waiting">等待</span>
      case 6: return <span className="agv-status status-error">错误</span>
      case 7: return <span className="agv-status status-returning">返回中</span>
      default: return <span className="agv-status status-idle">空闲</span>
    }
  }

  const batteryColor = (soc) => {
    if (soc == null) return '#94a3b8'
    if (soc < 10) return '#dc2626'
    if (soc < 20) return '#f97316'
    if (soc < 40) return '#eab308'
    if (soc < 60) return '#fbbf24'
    return '#22c55e'
  }

  const orderStatusLabel = (s) => {
    switch (s) {
      case 0: return <span className="order-status os-pending">待分配</span>
      case 1: return <span className="order-status os-assigned">已分配</span>
      case 2: return <span className="order-status os-progress">执行中</span>
      case 3: return <span className="order-status os-done">已完成</span>
      default: return <span className="order-status os-pending">未知</span>
    }
  }

  return (
    <div className="app">
      <div className="header">
        <div className="title">🤖 AGV 仓储中枢调度平台</div>
        <div className="connection-status">
          <span className={`indicator ${connected ? 'connected' : 'disconnected'}`} />
          <span>{connected ? '已连接' : '连接中...'}</span>
        </div>
        <div className="stats">
          <div className="stat-item">
            <span className="stat-label">AGV 总数</span>
            <span className="stat-value">{stats.total}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">运行中</span>
            <span className="stat-value moving">{stats.moving}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">空闲</span>
            <span className="stat-value idle">{stats.idle}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">充电</span>
            <span className="stat-value charging">{stats.charging}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">低电量</span>
            <span className="stat-value lowbat">{stats.lowBattery}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">回充</span>
            <span className="stat-value returning">{stats.returning}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">平均SOC</span>
            <span className="stat-value" style={{ color: batteryColor(stats.avgSOC) }}>{stats.avgSOC}%</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">平均温度</span>
            <span className="stat-value">{stats.avgTemp}°C</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">订单</span>
            <span className="stat-value orders">{orderStats.total}</span>
          </div>
          <div className="stat-item">
            <span className="stat-label">执行中</span>
            <span className="stat-value orders">{orderStats.progress}</span>
          </div>
        </div>
      </div>

      <div className="main">
        <div className="canvas-wrap">
          <WarehouseCanvas map={map} agvs={agvs} agvPositionsRef={agvPositionsRef} />
        </div>
        <div className="sidebar">
          <div className="sidebar-section">
            <div className="sidebar-title">图例</div>
            <div className="legend">
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#3b4252' }} />
                <span>货架</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#7c3aed' }} />
                <span>充电桩</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#0891b2' }} />
                <span>入库站台</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#c2410c' }} />
                <span>出库站台</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#fbbf24' }} />
                <span>AGV 空闲</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#34d399' }} />
                <span>AGV 移动中</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#22c55e', boxShadow: '0 0 8px #22c55e' }} />
                <span>AGV 充电中</span>
              </div>
              <div className="legend-item">
                <div className="legend-color" style={{ background: '#facc15', boxShadow: '0 0 8px #facc15' }} />
                <span>AGV 低电量/回充</span>
              </div>
            </div>
          </div>

          <div className="sidebar-section">
            <div className="sidebar-title">AGV 列表</div>
            <div className="agv-list agv-list-energy">
              {Object.values(agvs).sort((a, b) => a.id - b.id).map(a => (
                <div key={a.id} className={`agv-item ${a.isCharging ? 'agv-charging-row' : ''} ${a.isReturning || a.lowBattery ? 'agv-lowbat-row' : ''}`}>
                  <div className="agv-row-main">
                    <span className="agv-id">#{String(a.id).padStart(3, '0')}</span>
                    {statusLabel(a)}
                    <span className="agv-pos">({a.x},{a.y})</span>
                    {a.hasCargo && <span className="agv-cargo-tag">📦</span>}
                  </div>
                  <div className="agv-energy-row">
                    <div className="agv-battery-bar">
                      <div
                        className="agv-battery-fill"
                        style={{
                          width: `${Math.max(0, Math.min(100, a.battery ?? 0))}%`,
                          background: batteryColor(a.battery)
                        }}
                      />
                      <span className="agv-battery-text">{a.battery ?? '--'}%</span>
                    </div>
                    <span className="agv-temp" style={{ color: (a.temperature ?? 0) > 45 ? '#ef4444' : (a.temperature ?? 0) > 35 ? '#f59e0b' : '#94a3b8' }}>
                      🌡 {a.temperature != null ? a.temperature.toFixed(1) : '--'}°C
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </div>

          <div className="sidebar-section">
            <div className="sidebar-title">订单列表</div>
            <div className="order-list">
              {Object.values(orders).sort((a, b) => b.id - a.id).slice(0, 30).map(o => (
                <div key={o.id} className="order-item">
                  <div className="order-header">
                    <span className="order-id">#{o.id}</span>
                    <span className={`order-type ${o.type === 1 ? 'order-in' : 'order-out'}`}>
                      {o.type === 1 ? '入库' : '出库'}
                    </span>
                    {orderStatusLabel(o.status)}
                  </div>
                  <div className="order-route">
                    ({o.pickupX},{o.pickupY}) → ({o.dropoffX},{o.dropoffY})
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
