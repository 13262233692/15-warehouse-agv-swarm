import { useMemo } from 'react'
import { useWebSocket } from './hooks/useWebSocket.js'
import WarehouseCanvas from './components/WarehouseCanvas.jsx'

export default function App() {
  const { connected, map, agvs, orders, agvPositionsRef } = useWebSocket('ws://localhost:8080/ws')

  const stats = useMemo(() => {
    let moving = 0
    let idle = 0
    let withCargo = 0
    const list = Object.values(agvs)
    list.forEach(a => {
      if (a.status === 1) moving++
      else if (a.status === 0 || a.status === undefined) idle++
      if (a.hasCargo) withCargo++
    })
    return { moving, idle, withCargo, total: list.length }
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

  const statusLabel = (s) => {
    switch (s) {
      case 0: return <span className="agv-status status-idle">空闲</span>
      case 1: return <span className="agv-status status-moving">移动</span>
      case 2: return <span className="agv-status status-picking">取货</span>
      case 3: return <span className="agv-status status-placing">放货</span>
      case 4: return <span className="agv-status status-charging">充电</span>
      default: return <span className="agv-status status-idle">空闲</span>
    }
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
            <span className="stat-label">载货</span>
            <span className="stat-value cargo">{stats.withCargo}</span>
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
            </div>
          </div>

          <div className="sidebar-section">
            <div className="sidebar-title">AGV 列表</div>
            <div className="agv-list">
              {Object.values(agvs).sort((a, b) => a.id - b.id).map(a => (
                <div key={a.id} className="agv-item">
                  <span className="agv-id">#{String(a.id).padStart(3, '0')}</span>
                  {statusLabel(a.status)}
                  <span className="agv-pos">({a.x},{a.y})</span>
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
