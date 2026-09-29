import React, { createContext, useContext, useState, useCallback } from 'react'

// ---------------- 状态徽标 ----------------
export const KEG_STATUS = {
  IN_STOCK: ['酒厂仓库', 'green'], IN_TRANSIT: ['配送中', 'blue'], AT_BAR: ['在店', 'blue'],
  EMPTY_RESERVED: ['空桶待回收', 'amber'], PICKED: ['司机已收', 'purple'],
  AT_WASH: ['清洗中', 'purple'], WASH_FAIL: ['清洗不合格', 'red'], LOST: ['丢失', 'red'],
  SCRAPPED: ['报废', 'gray']
}
export const STAGE = {
  DELIVERED: ['已配送未开桶', 'blue'], TAPPED: ['已开桶', 'amber'], EMPTY: ['已打空', 'amber'],
  RESERVED: ['已预约回收', 'amber'], RETURNED: ['已回收', 'purple'],
  WASHED: ['清洗合格', 'green'], FAILED: ['清洗不合格', 'red']
}
export const DEPOSIT = {
  HELD: ['押金占用中', 'amber'], RELEASED: ['已释放', 'green'],
  DEDUCTED: ['已扣款', 'red'], PARTIAL: ['部分扣款', 'amber']
}
export const BATCH_STATUS = {
  NORMAL: ['正常', 'green'], SUSPENDED: ['暂停销售', 'red'],
  RECALLED: ['召回中', 'red'], CLEARED: ['已解除', 'gray']
}
export const RES_STATUS = { WAITING: ['待取桶', 'amber'], PICKED: ['已取桶', 'green'], MISSED: ['爽约/逾期', 'red'], CANCELLED: ['已取消', 'gray'] }
export const REWORK_STATUS = {
  OPEN: ['返修中', 'red'], REWORKED: ['待仓库确认', 'amber'],
  CONFIRMED: ['已恢复灌装', 'green'], SCRAPPED: ['已报废', 'gray']
}
export const REWORK_DEFECT = {
  ODOR: '桶内异味', VALVE_DAMAGED: '阀门损坏', DEFORMED: '桶体变形',
  RESIDUE: '内壁残液', OTHER: '其他缺陷'
}
export const BILL_STATUS = { PENDING: ['待财务确认', 'amber'], CONFIRMED: ['已入账', 'green'] }
export const AUDIENCE = { BAR: '已收货酒吧', PENDING_ORDER: '待配送订单', DEALER_STOCK: '经销商库存', ALL: '全渠道' }

export function Badge({ map, value }) {
  const [text, cls] = map[value] || [value, 'gray']
  return <span className={'badge ' + cls}>{text}</span>
}

// ---------------- Toast ----------------
const ToastCtx = createContext(null)
export function useToast() { return useContext(ToastCtx) }
export function ToastProvider({ children }) {
  const [t, setT] = useState(null)
  const show = useCallback((msg, kind = 'ok') => {
    setT({ msg, kind })
    setTimeout(() => setT(null), 3200)
  }, [])
  return (
    <ToastCtx.Provider value={show}>
      {children}
      {t && <div className={'toast ' + t.kind}>{t.msg}</div>}
    </ToastCtx.Provider>
  )
}

// ---------------- Modal ----------------
export function Modal({ title, onClose, children, wide }) {
  return (
    <div className="modal-mask" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="modal" style={wide ? { width: 900 } : undefined}>
        <div className="row" style={{ marginBottom: 12 }}>
          <h2 style={{ margin: 0 }}>{title}</h2>
          <span className="spacer" />
          <button className="subtle small" onClick={onClose}>关闭 ✕</button>
        </div>
        {children}
      </div>
    </div>
  )
}

// ---------------- 小组件 ----------------
export function Money({ v }) {
  return <>¥{Number(v || 0).toFixed(0)}</>
}

export function Flags({ items }) {
  return (
    <div className="flags">
      {items.filter(Boolean).map((x, i) => <span key={i} className="badge red">{x}</span>)}
      {items.filter(Boolean).length === 0 && <span className="muted small">—</span>}
    </div>
  )
}

export function Stat({ label, value, kind = '', sub }) {
  return (
    <div className={'panel stat ' + kind}>
      <span className="num">{value}</span>
      <span className="lbl">{label}</span>
      {sub && <span className="small muted">{sub}</span>}
    </div>
  )
}

export function Empty({ text = '暂无数据' }) {
  return <div className="muted small" style={{ padding: '20px 0', textAlign: 'center' }}>{text}</div>
}

export function Th({ children, w }) {
  return <th style={w ? { width: w } : undefined}>{children}</th>
}
