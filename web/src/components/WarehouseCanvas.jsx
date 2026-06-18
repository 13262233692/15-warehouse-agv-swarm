import { useEffect, useRef } from 'react'

const GRID_SIZE = 100
const CELL_SHELF = 1
const CELL_CHARGING = 2
const CELL_DOCKING_IN = 3
const CELL_DOCKING_OUT = 4

const COLOR_BG = '#05070d'
const COLOR_GRID = '#111827'
const COLOR_SHELF = '#3b4252'
const COLOR_SHELF_EDGE = '#4b5563'
const COLOR_CHARGING = '#7c3aed'
const COLOR_DOCK_IN = '#0891b2'
const COLOR_DOCK_OUT = '#c2410c'
const COLOR_AGV_IDLE = '#fbbf24'
const COLOR_AGV_MOVING = '#34d399'
const COLOR_AGV_PICK = '#60a5fa'
const COLOR_AGV_PLACE = '#a78bfa'
const COLOR_CARGO = '#ef4444'
const COLOR_LOW_BATTERY = '#facc15'
const COLOR_CHARGING_STATE = '#22c55e'
const COLOR_RETURNING = '#eab308'

function getAGVBaseColor(status) {
  switch (status) {
    case 1: return COLOR_AGV_MOVING
    case 2: return COLOR_AGV_PICK
    case 3: return COLOR_AGV_PLACE
    case 4: return COLOR_AGV_PLACE
    default: return COLOR_AGV_IDLE
  }
}

function getBatteryColor(soc) {
  if (soc < 10) return '#dc2626'
  if (soc < 20) return '#f97316'
  if (soc < 40) return '#eab308'
  if (soc < 60) return '#fbbf24'
  return '#22c55e'
}

export default function WarehouseCanvas({ map, agvs, agvPositionsRef
}) {
  const canvasRef = useRef(null)
  const containerRef = useRef(null)
  const animRef = useRef(null)
  const stateRef = useRef({ map: [], agvs: {}, cellSize: 8, offsetX: 0, offsetY: 0 })

  useEffect(() => {
    stateRef.current.map = map
  }, [map])

  useEffect(() => {
    stateRef.current.agvs = agvs
  }, [agvs])

  useEffect(() => {
    const canvas = canvasRef.current
    const ctx = canvas.getContext('2d')
    const container = containerRef.current

    function resize() {
      const w = container.clientWidth
      const h = container.clientHeight
      canvas.width = w * window.devicePixelRatio
      canvas.height = h * window.devicePixelRatio
      canvas.style.width = w + 'px'
      canvas.style.height = h + 'px'
      ctx.setTransform(window.devicePixelRatio, 0, 0, window.devicePixelRatio, 0, 0)

      const maxW = w / GRID_SIZE
      const maxH = h / GRID_SIZE
      const cs = Math.max(4, Math.floor(Math.min(maxW, maxH) - 1))
      stateRef.current.cellSize = cs
      stateRef.current.offsetX = (w - cs * GRID_SIZE) / 2
      stateRef.current.offsetY = (h - cs * GRID_SIZE) / 2
    }

    resize()
    const ro = new ResizeObserver(resize)
    ro.observe(container)

    function render() {
      const cellSize = stateRef.current.cellSize
      const ox = stateRef.current.offsetX
      const oy = stateRef.current.offsetY
      const w = container.clientWidth
      const h = container.clientHeight

      ctx.fillStyle = COLOR_BG
      ctx.fillRect(0, 0, w, h)

      ctx.strokeStyle = COLOR_GRID
      ctx.lineWidth = 0.5
      for (let i = 0; i <= GRID_SIZE; i++) {
        ctx.beginPath()
        ctx.moveTo(ox + i * cellSize, oy)
        ctx.lineTo(ox + i * cellSize, oy + GRID_SIZE * cellSize)
        ctx.stroke()
        ctx.beginPath()
        ctx.moveTo(ox, oy + i * cellSize)
        ctx.lineTo(ox + GRID_SIZE * cellSize, oy + i * cellSize)
        ctx.stroke()
      }

      const curMap = stateRef.current.map
      for (let i = 0; i < curMap.length; i++) {
        const c = curMap[i]
        let color
        if (c.t === CELL_SHELF) color = COLOR_SHELF
        else if (c.t === CELL_CHARGING) color = COLOR_CHARGING
        else if (c.t === CELL_DOCKING_IN) color = COLOR_DOCK_IN
        else if (c.t === CELL_DOCKING_OUT) color = COLOR_DOCK_OUT
        if (color) {
          ctx.fillStyle = color
          ctx.fillRect(ox + c.x * cellSize, oy + c.y * cellSize, cellSize, cellSize)
          if (c.t === CELL_SHELF) {
            ctx.strokeStyle = COLOR_SHELF_EDGE
            ctx.lineWidth = 0.5
            ctx.strokeRect(ox + c.x * cellSize, oy + c.y * cellSize, cellSize, cellSize)
          }
        }
      }

      const positions = agvPositionsRef ? agvPositionsRef.current : stateRef.current.agvs
      const now = performance.now()
      const agvList = Object.values(positions || {})

      agvList.forEach(a => {
        let x = a.smoothX != null ? a.smoothX : a.x
        let y = a.smoothY != null ? a.smoothY : a.y
        if (a.updatedAt && a.prevX != null) {
          const t = Math.min(1, (now - a.updatedAt) / 80)
          x = a.prevX + (a.x - a.prevX) * t
          y = a.prevY + (a.y - a.prevY) * t
        }
        const px = ox + (x + 0.5) * cellSize
        const py = oy + (y + 0.5) * cellSize
        const r = cellSize * 0.45

        const isCharging = a.isCharging
        const isReturning = a.isReturning
        const lowBattery = a.lowBattery || a.lowbat

        let agvColor = getAGVBaseColor(a.status)
        let glow = 6
        let blinkAlpha = 1
        let ringEnabled = false
        let ringColor = null

        if (isCharging) {
          agvColor = COLOR_CHARGING_STATE
          glow = 18
          ringEnabled = true
          ringColor = '#22c55e'
          const breath = 0.5 + 0.5 * Math.sin(now / 600)
          glow = 10 + breath * 18
        } else if (isReturning || lowBattery) {
          agvColor = COLOR_LOW_BATTERY
          glow = 14
          const blinkPhase = (Math.sin(now / 150) + 1) / 2
          blinkAlpha = 0.55 + blinkPhase * 0.45
          ringEnabled = true
          ringColor = '#eab308'
          glow = 8 + blinkPhase * 20
        }

        if (ringEnabled && ringColor) {
          ctx.save()
          const ringR = r + cellSize * 0.35
          const ringAlpha = 0.25 + Math.abs(Math.sin(now / 200)) * 0.45
          ctx.strokeStyle = ringColor
          ctx.globalAlpha = ringAlpha
          ctx.lineWidth = cellSize * 0.18
          ctx.beginPath()
          ctx.arc(px, py, ringR, 0, Math.PI * 2)
          ctx.stroke()

          if (isCharging) {
            ctx.strokeStyle = '#4ade80'
            ctx.globalAlpha = 0.15 + Math.abs(Math.sin(now / 350)) * 0.3
            ctx.lineWidth = cellSize * 0.1
            ctx.beginPath()
            ctx.arc(px, py, ringR + cellSize * 0.3, 0, Math.PI * 2)
            ctx.stroke()
          }
          ctx.restore()
        }

        ctx.save()
        ctx.shadowColor = agvColor
        ctx.shadowBlur = glow
        ctx.globalAlpha = blinkAlpha
        ctx.fillStyle = agvColor
        ctx.beginPath()
        ctx.arc(px, py, r, 0, Math.PI * 2)
        ctx.fill()
        ctx.restore()

        if (a.hasCargo) {
          ctx.fillStyle = COLOR_CARGO
          ctx.fillRect(px - r * 0.5, py - r * 0.5, r, r)
        }

        if (isCharging) {
          ctx.fillStyle = '#86efac'
          ctx.font = `bold ${Math.max(9, cellSize * 0.7)}px sans-serif`
          ctx.textAlign = 'center'
          ctx.textBaseline = 'middle'
          ctx.fillText('⚡', px, py - cellSize * 0.65)
        } else if (isReturning || lowBattery) {
          ctx.fillStyle = '#fde047'
          ctx.font = `bold ${Math.max(8, cellSize * 0.6)}px sans-serif`
          ctx.textAlign = 'center'
          ctx.textBaseline = 'middle'
          ctx.fillText('🔋', px, py - cellSize * 0.65)
        }

        if (a.battery != null) {
          const batW = cellSize * 1.4
          const batH = cellSize * 0.35
          const batX = px - batW / 2
          const batY = py + r + cellSize * 0.25
          ctx.fillStyle = '#1e293b'
          ctx.fillRect(batX, batY, batW, batH)
          ctx.strokeStyle = '#475569'
          ctx.lineWidth = 0.8
          ctx.strokeRect(batX, batY, batW, batH)
          const fillW = (batW - 2) * Math.max(0, Math.min(1, a.battery / 100))
          ctx.fillStyle = getBatteryColor(a.battery)
          ctx.fillRect(batX + 1, batY + 1, fillW, batH - 2)
          ctx.fillStyle = '#f1f5f9'
          ctx.font = `${Math.max(7, cellSize * 0.38)}px monospace`
          ctx.textAlign = 'center'
          ctx.textBaseline = 'middle'
          ctx.fillText(`${a.battery}%`, px, batY + batH / 2)
        }

        ctx.fillStyle = '#0a0e1a'
        ctx.font = `${Math.max(8, cellSize * 0.6)}px monospace`
        ctx.textAlign = 'center'
        ctx.textBaseline = 'middle'
        ctx.fillText(String(a.id), px, py)
      })

      animRef.current = requestAnimationFrame(render)
    }

    animRef.current = requestAnimationFrame(render)

    return () => {
      cancelAnimationFrame(animRef.current)
      ro.disconnect()
    }
  }, [agvPositionsRef])

  return (
    <div ref={containerRef} className="canvas-wrap" style={{ position: 'absolute', inset: 0 }}>
      <canvas ref={canvasRef} />
    </div>
  )
}
