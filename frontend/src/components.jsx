import React, { createContext, useContext, useState, useCallback } from 'react'

// ---------------- 状态徽标 ----------------
export const KEG_STATUS = {
  IN_STOCK: ['酒厂仓库', 'green'], IN_TRANSIT: ['配送中', 'blue'], AT_BAR: ['在店', 'blue'],
  EMPTY_RESERVED: ['空桶待回收', 'amber'], PICKED: ['司机已收', 'purple'],
  AT_WASH: ['清洗中', 'purple'], WASH_FAIL: ['清洗不合格', 'red'], REPAIR: ['不合格返修中', 'red'],
  LOST: ['丢失', 'red'],
  SCRAPPED: ['报废', 'gray']
}
export const STAGE = {
  DELIVERED: ['已配送未开桶', 'blue'], TAPPED: ['已开桶', 'amber'], EMPTY: ['已打空', 'amber'],
  RESERVED: ['已预约回收', 'amber'], RETURNED: ['已回收', 'purple'],
  WASHED: ['清洗合格', 'green'], FAILED: ['清洗不合格', 'red'], REPAIRING: ['不合格返修中', 'red']
}
export const DEPOSIT = {
  HELD: ['押金占用中', 'amber'], RELEASED: ['已释放', 'green'],
  DEDUCTED: ['已扣款', 'red'], PARTIAL: ['部分扣款', 'amber']
}
export const REPAIR_STATUS = {
  OPEN: ['返修中', 'red'], SUBMITTED: ['待仓库确认', 'amber'],
  CONFIRMED: ['复检合格·已回库', 'green'], REJECTED: ['复检退回', 'red'], SCRAPPED: ['报废', 'gray']
}
export const CHARGE_TYPE = { REPAIR_FEE: '返修费用', DEPOSIT_DEDUCT: '押金扣减' }
export const CHARGE_STATUS = { PENDING: ['待财务确认', 'amber'], CONFIRMED: ['已确认入账', 'green'], VOID: ['已作废', 'gray'] }
export const PARTY_TYPE = { BAR: '酒吧', WASHPLANT: '清洗厂' }
export const BATCH_STATUS = {
  NORMAL: ['正常', 'green'], SUSPENDED: ['暂停销售', 'red'],
  RECALLED: ['召回中', 'red'], CLEARED: ['已解除', 'gray']
}
export const RES_STATUS = { WAITING: ['待取桶', 'amber'], PICKED: ['已取桶', 'green'], MISSED: ['爽约/逾期', 'red'], CANCELLED: ['已取消', 'gray'] }
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

// 照片读取为 base64（data URL），供返修缺陷/复检上传
export function readFileAsDataURL(file) {
  return new Promise((resolve, reject) => {
    const fr = new FileReader()
    fr.onload = () => resolve(fr.result)
    fr.onerror = reject
    fr.readAsDataURL(file)
  })
}

// 多图上传：产出 [{name, content_type, data}]
export function PhotoUploader({ photos, setPhotos, hint }) {
  const onPick = async (files) => {
    for (const f of Array.from(files)) {
      if (!f.type.startsWith('image/')) continue
      if (f.size > 3 * 1024 * 1024) { alert(`${f.name} 超过 3MB`); continue }
      const dataUrl = await readFileAsDataURL(f)
      const [head, data] = dataUrl.split(',')
      const ctype = (head.match(/data:(.*?);/) || [])[1] || 'image/png'
      setPhotos(x => [...x, { name: f.name, content_type: ctype, data, _preview: dataUrl }])
    }
  }
  return (
    <div>
      <label className="ghost small" style={{ display: 'inline-block', cursor: 'pointer', border: '1px dashed var(--line)', padding: '6px 12px', borderRadius: 8 }}>
        ＋ 上传桶体照片（仓库留档，必传）
        <input type="file" accept="image/*" multiple style={{ display: 'none' }}
          onChange={e => { onPick(e.target.files); e.target.value = '' }} />
      </label>
      {hint && <div className="small muted" style={{ marginTop: 4 }}>{hint}</div>}
      <div className="photogrid" style={{ marginTop: 8 }}>
        {photos.map((p, i) => (
          <div key={i} className="photochip">
            <img src={p._preview || ('data:' + p.content_type + ';base64,' + p.data)} alt={p.name} />
            <button type="button" className="photo-del" onClick={() => setPhotos(x => x.filter((_, j) => j !== i))}>✕</button>
          </div>
        ))}
      </div>
    </div>
  )
}

// 照片展示（返修详情，点击新窗口看原图）
export function PhotoGallery({ photos }) {
  if (!photos || photos.length === 0) return <span className="muted small">暂无照片</span>
  return (
    <div className="photogrid">
      {photos.map(p => (
        <a key={p.id} className="photochip" href={p.data_url} target="_blank" rel="noreferrer" title={p.file_name}>
          <img src={p.data_url} alt={p.file_name} />
          <span className="photo-cap">{p.stage === 'DEFECT' ? '缺陷取证' : '返修复检'}</span>
        </a>
      ))}
    </div>
  )
}
