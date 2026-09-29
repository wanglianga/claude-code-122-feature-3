import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { Badge, KEG_STATUS, STAGE, DEPOSIT, Modal, Flags, Empty } from '../components.jsx'

const ASSET_OPTS = [['', '全部资产状态'], ['IN_STOCK', '酒厂仓库'], ['AT_BAR', '在店'], ['EMPTY_RESERVED', '空桶待回收'],
  ['PICKED', '司机已收'], ['WASH_FAIL', '清洗不合格'], ['REPAIR', '不合格返修中'], ['LOST', '丢失']]

export default function Kegs() {
  const [list, setList] = useState([])
  const [status, setStatus] = useState('')
  const [q, setQ] = useState('')
  const [trace, setTrace] = useState(null)
  const [regOpen, setRegOpen] = useState(false)

  const load = () => {
    const p = new URLSearchParams()
    if (status) p.set('status', status)
    if (q) p.set('q', q)
    api('/kegs?' + p.toString()).then(d => setList(d.kegs))
  }
  useEffect(load, [status])

  const openTrace = (code) => api('/kegs/trace/' + code).then(setTrace)

  return (
    <div>
      <div className="row" style={{ marginBottom: 14 }}>
        <h1 style={{ margin: 0 }}>酒桶资产与追溯</h1>
        <span className="spacer" />
        <button className="ghost" onClick={() => setRegOpen(true)}>登记新桶</button>
      </div>

      <div className="toolbar">
        <div className="f"><label>桶编号搜索</label>
          <input placeholder="如 KEG-0011" value={q} onChange={e => setQ(e.target.value)}
            onKeyDown={e => e.key === 'Enter' && load()} /></div>
        <div className="f"><label>资产状态</label>
          <select value={status} onChange={e => setStatus(e.target.value)}>
            {ASSET_OPTS.map(([v, t]) => <option key={v} value={v}>{t}</option>)}
          </select></div>
        <button onClick={load}>查询</button>
      </div>

      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>桶编号</th><th>容量</th><th>资产状态</th><th>当前所在</th><th>批次/酒款</th>
            <th>阶段</th><th>押金</th><th>操作</th>
          </tr></thead>
          <tbody>
            {list.map(k => (
              <tr key={k.id}>
                <td><code className="kcode">{k.keg_code}</code>{!k.refillable && <span className="badge red" style={{ marginLeft: 6 }}>禁灌装</span>}</td>
                <td>{k.capacity_liters}L</td>
                <td><Badge map={KEG_STATUS} value={k.asset_status} /></td>
                <td>{k.bar || <span className="muted">—</span>}</td>
                <td>{k.batch_no ? <>{k.batch_no} <span className="muted">{k.style}</span></> : <span className="muted">空桶</span>}</td>
                <td>{k.stage ? <Badge map={STAGE} value={k.stage} /> : '—'}</td>
                <td>{k.deposit_status ? <Badge map={DEPOSIT} value={k.deposit_status} /> : '—'}</td>
                <td><button className="small ghost" onClick={() => openTrace(k.keg_code)}>全链路追溯</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {list.length === 0 && <Empty />}
      </div>

      {trace && <TraceModal d={trace} onClose={() => setTrace(null)} />}
      {regOpen && <RegisterModal onClose={() => setRegOpen(false)} onDone={() => { setRegOpen(false); load() }} />}
    </div>
  )
}

function TraceModal({ d, onClose }) {
  const c = d.current || {}
  const flags = [c.late_return && '延迟归还', c.missing_scan && '司机漏扫', c.lost && '酒桶丢失', c.deformed && '桶体变形']
  return (
    <Modal wide title={`桶追溯 · ${d.keg.keg_code}`} onClose={onClose}>
      <div className="grid cols-2">
        <div>
          <h3>资产信息</h3>
          <dl className="kv">
            <dt>容量</dt><dd>{d.keg.capacity_liters} L</dd>
            <dt>资产状态</dt><dd><Badge map={KEG_STATUS} value={d.keg.asset_status} /></dd>
            <dt>再灌装资格</dt><dd>{d.keg.refillable ? <span className="badge green">允许</span> : <span className="badge red">取消</span>}</dd>
            {c.cycle_id && <>
              <dt>酒液批次</dt><dd>{c.batch_no} · {c.beer}（{c.style}）</dd>
              <dt>所在酒吧</dt><dd>{c.bar || '—'}</dd>
              <dt>收货人</dt><dd>{c.bar_receiver || '—'}</dd>
              <dt>当前阶段</dt><dd><Badge map={STAGE} value={c.stage} /></dd>
              <dt>押金 / 账期</dt><dd>¥{c.deposit_amount} · 到期 {c.deposit_due_date}</dd>
              <dt>押金状态</dt><dd><Badge map={DEPOSIT} value={c.deposit_status} /></dd>
              {c.deposit_status !== 'HELD' && <>
                <dt>已释放</dt><dd>¥{c.released_amount}</dd>
                <dt>已扣款</dt><dd>¥{c.deducted_amount}{c.deduct_reason ? `（${c.deduct_reason}）` : ''}</dd>
              </>}
              <dt>异常标记</dt><dd><Flags items={flags} /></dd>
            </>}
          </dl>
        </div>
        <div>
          <h3>事件时间线</h3>
          <ul className="timeline">
            {(d.events || []).map((e, i) => (
              <li key={i}>
                <div className="et">{EVENT_NAME[e.event_type] || e.event_type}</div>
                <div className="em">{e.at} · {e.actor}</div>
                {Object.keys(e.detail || {}).length > 0 && <pre>{JSON.stringify(e.detail, null, 1)}</pre>}
              </li>
            ))}
            {(!d.events || d.events.length === 0) && <div className="muted small">尚无事件</div>}
          </ul>
        </div>
      </div>
    </Modal>
  )
}

const EVENT_NAME = {
  DELIVERED: '配送签收', TAPPED: '开桶售酒', TAP_FEEDBACK: '开桶反馈', EMPTIED: '酒桶打空',
  RETURN_RESERVED: '预约回收', PICKED: '司机扫码回收', MISSING_SCAN: '司机漏扫（审计）',
  WASHED: '清洗检测合格·回库', WASH_FAILED: '清洗检测不合格',
  REPAIR_CREATED: '发起不合格返修（暂停灌装）', REPAIR_SUBMITTED: '返修完成·上传复检',
  REPAIR_CONFIRMED: '仓库复检合格·恢复灌装', REPAIR_REJECTED: '仓库复检退回',
  BILLING_CONFIRMED: '返修账单财务确认',
  DEPOSIT_SETTLED: '押金结算', MARKED_LOST: '登记丢失'
}

function RegisterModal({ onClose, onDone }) {
  const [count, setCount] = useState(5)
  const [cap, setCap] = useState(30)
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    setBusy(true)
    try {
      const d = await api('/kegs/register', { method: 'POST', body: { count: Number(count), capacity_liters: Number(cap) } })
      alert(`已登记 ${d.count} 只：${d.created.join('、')}`)
      onDone()
    } finally { setBusy(false) }
  }
  return (
    <Modal title="酒厂登记新桶" onClose={onClose}>
      <div className="grid cols-2">
        <div><label>数量</label><input type="number" value={count} min={1} max={200} onChange={e => setCount(e.target.value)} /></div>
        <div><label>容量（升）</label>
          <select value={cap} onChange={e => setCap(e.target.value)}>
            <option value={20}>20 L</option><option value={30}>30 L</option><option value={50}>50 L</option>
          </select></div>
      </div>
      <div className="row end" style={{ marginTop: 16 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button disabled={busy} onClick={submit}>确认登记</button>
      </div>
    </Modal>
  )
}
