import { useEffect, useRef, useState } from 'react'

export function useWebSocket(url) {
  const [connected, setConnected] = useState(false)
  const [map, setMap] = useState([])
  const [agvs, setAgvs] = useState({})
  const [orders, setOrders] = useState({})
  const wsRef = useRef(null)
  const reconnectRef = useRef(null)
  const agvPositionsRef = useRef({})

  useEffect(() => {
    let closed = false

    const connect = () => {
      try {
        const ws = new WebSocket(url)
        wsRef.current = ws

        ws.onopen = () => {
          setConnected(true)
        }

        ws.onclose = () => {
          setConnected(false)
          if (!closed) {
            reconnectRef.current = setTimeout(connect, 1000)
          }
        }

        ws.onerror = () => {
          ws.close()
        }

        ws.onmessage = (e) => {
          try {
            const msg = JSON.parse(e.data)
            if (msg.type === 'map') {
              setMap(msg.payload)
            } else if (msg.type === 'agvs') {
              const smooth = {}
              const now = performance.now()
              msg.payload.forEach(a => {
                const prev = agvPositionsRef.current[a.id]
                if (prev) {
                  const dx = a.x - prev.x
                  const dy = a.y - prev.y
                  const dist = Math.sqrt(dx * dx + dy * dy)
                  if (dist > 0.1) {
                    smooth[a.id] = {
                      ...a,
                      smoothX: prev.x + dx * 0.8,
                      smoothY: prev.y + dy * 0.8,
                      prevX: prev.x,
                      prevY: prev.y,
                      updatedAt: now,
                    }
                  } else {
                    smooth[a.id] = { ...a, smoothX: a.x, smoothY: a.y, updatedAt: now }
                  }
                } else {
                  smooth[a.id] = { ...a, smoothX: a.x, smoothY: a.y, updatedAt: now }
                }
              })
              agvPositionsRef.current = smooth
              setAgvs(smooth)
            } else if (msg.type === 'orders') {
              const oMap = {}
              msg.payload.forEach(o => { oMap[o.id] = o })
              setOrders(oMap)
            }
          } catch (err) {
            console.error('WS parse error', err)
          }
        }
      } catch (err) {
        reconnectRef.current = setTimeout(connect, 2000)
      }
    }

    connect()

    return () => {
      closed = true
      if (reconnectRef.current) clearTimeout(reconnectRef.current)
      if (wsRef.current) wsRef.current.close()
    }
  }, [url])

  return { connected, map, agvs, orders, agvPositionsRef }
}
