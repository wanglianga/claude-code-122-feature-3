import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { Badge, STAGE, DEPOSIT, BATCH_STATUS, Modal, useToast, Flags, Empty, Money, CHARGE_TYPE, CHARGE_STATUS } from '../components.jsx'

export default function BarPage() {
  const [cycles, setCycles] = useState([])
  const [barId, setBarId] = useState(null)
  const [fb, setFb] = useState(null)
  const [reserve, setReserve] = useState([])
  const [reserveOpen, setReserveOpen] = useState(false)
  const [billingOpen, setBillingOpen] = useState(false)
  const [billCount, setBillCount] = useState(0)
  const toast = useToast()

  const load = () => api('/bar/cycles').then(d => { setCycles(d.cycles); setBarId(d.bar_id) })
  const loadBillingCount = () => api('/bar/billing').then(d =>
    setBillCount((d.charges || []).filter(c => c.status === 'PENDING').length))
  useEffect(() => { load(); loadBillingCount() }, [])

  const toggleReserve = (id) => setReserve(x => x.includes(id) ? x.filter(v => v !== id) : [...x, id])

  return (
    <div>
      <h1>我的在店酒桶</h1>
      <div className="muted small" style={{ marginBottom: 12 }}>
        开桶后请反馈出酒速度、气压、口味异常与客诉；打空后预约回收。停售批次的酒桶会有红色提醒，请勿继续售卖。
      </div>

      <div className="row" style={{ marginBottom: 10 }}>
        <button className="ghost" disabled={reserve.length === 0} onClick={() => setReserveOpen(true)}>
          预约回收（已选 {reserve.length} 只空桶）
        </button>
        <span className="spacer" />
        <button className="subtle" onClick={() => setBillingOpen(true)}>
          本店返修账单{billCount > 0 && <span className="badge red" style={{ marginLeft: 6 }}>{billCount} 待确认</span>}
        </button>
        <button className="subtle" onClick={load}>刷新</button>
      </div>

      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>选择回收</th><th>桶编号</th><th>酒款/批次</th><th>阶段</th><th>押金/账期</th>
            <th>状态提醒</th><th>操作</th>
          </tr></thead>
          <tbody>
            {cycles.map(c => {
              const actionable = ['TAPPED', 'EMPTY'].includes(c.stage)
              return (
                <tr key={c.cycle_id}>
                  <td>
                    {c.stage === 'EMPTY'
                      ? <input type="checkbox" style={{ width: 'auto' }} checked={reserve.includes(c.cycle_id)}
                          onChange={() => toggleReserve(c.cycle_id)} />
                      : <span className="muted small">—</span>}
                  </td>
                  <td><code className="kcode">{c.keg_code}</code><div className="small muted">{c.capacity_liters}L</div></td>
                  <td>{c.beer}（{c.style}）<div className="small muted">{c.batch_no}</div>
                    {c.batch_status !== 'NORMAL' && <span className="badge red">批次{c.batch_status === 'SUSPENDED' ? '已停售' : '召回中'}·停止售卖</span>}
                  </td>
                  <td><Badge map={STAGE} value={c.stage} /></td>
                  <td>¥{c.deposit_amount}<div className={'small ' + (c.overdue ? '' : 'muted')} style={c.overdue ? { color: 'var(--danger)' } : undefined}>
                    账期至 {c.deposit_due_date}{c.overdue ? ' · 已逾期' : ''}
                  </div></td>
                  <td><Flags items={[c.overdue && '账期逾期', c.late_return && '延迟归还', c.missing_scan && '司机漏扫', c.lost && '丢失']} /></td>
                  <td>
                    {actionable && <button className="small ghost" onClick={() => setFb(c)}>开桶反馈</button>}
                    {c.stage === 'TAPPED' &&
                      <button className="small" style={{ marginLeft: 6 }}
                        onClick={async () => { try { await api('/cycles/empty/' + c.cycle_id, { method: 'POST' }); toast('已标记打空，可预约回收'); load() } catch (e) { toast(e.message, 'err') } }}>
                        标记打空
                      </button>}
                    {['RESERVED', 'RETURNED', 'WASHED', 'FAILED', 'REPAIRING'].includes(c.stage) &&
                      (c.stage === 'REPAIRING'
                        ? <span className="badge red">清洗不合格返修中·暂停再灌装</span>
                        : <span className="muted small">回收流程中</span>)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
        {cycles.length === 0 && <Empty text="本店当前没有在店酒桶" />}
      </div>

      {fb && <FeedbackModal c={fb} onClose={() => setFb(null)} onDone={() => { setFb(null); load() }} />}
      {reserveOpen && <ReserveModal ids={reserve} onClose={() => setReserveOpen(false)}
        onDone={() => { setReserveOpen(false); setReserve([]); load() }} />}
      {billingOpen && <BarBillingModal onClose={() => { setBillingOpen(false); loadBillingCount() }} />}
    </div>
  )
}

function BarBillingModal({ onClose }) {
  const [d, setD] = useState(null)
  useEffect(() => { api('/bar/billing').then(setD) }, [])
  return (
    <Modal wide title="本店返修账单" onClose={onClose}>
      <div className="muted small" style={{ marginBottom: 10 }}>
        归还酒桶在清洗厂检测中发现异味/阀门损坏等缺陷而返修时，返修费用与押金扣减进入本店账单；财务确认后正式入账。
      </div>
      {d && <div className="grid cols-2" style={{ marginBottom: 10 }}>
        <div className="panel stat warn"><span className="num">¥{d.summary.pending_amount}</span><span className="lbl">待财务确认（{d.summary.pending_count} 条）</span></div>
        <div className="panel stat ok"><span className="num">¥{d.summary.confirmed_amount}</span><span className="lbl">已确认入账（{d.summary.confirmed_count} 条）</span></div>
      </div>}
      <div className="tablewrap">
        <table className="minitable">
          <thead><tr><th>桶/返修单</th><th>类型</th><th>金额</th><th>摘要</th><th>状态</th><th>时间</th></tr></thead>
          <tbody>
            {(d?.charges || []).map(c => (
              <tr key={c.id}>
                <td><code className="kcode">{c.keg_code}</code><div className="small muted">{c.repair_no}</div></td>
                <td>{CHARGE_TYPE[c.charge_type]}</td>
                <td><Money v={c.amount} /></td>
                <td className="small muted">{c.memo}</td>
                <td><Badge map={CHARGE_STATUS} value={c.status} /></td>
                <td className="small muted">{c.confirmed_at || c.created_at}</td>
              </tr>
            ))}
            {d && d.charges.length === 0 && <tr><td colSpan={6}><Empty text="本店暂无返修账单" /></td></tr>}
          </tbody>
        </table>
      </div>
    </Modal>
  )
}

function FeedbackModal({ c, onClose, onDone }) {
  const [f, setF] = useState({ pour_speed: '正常', pressure: '正常', taste_off: false, taste_note: '', complaint: '', reported_by: '' })
  const set = (k, v) => setF(x => ({ ...x, [k]: v }))
  const toast = useToast()
  const submit = async () => {
    try {
      const d = await api('/cycles/feedback/' + c.cycle_id, { method: 'POST', body: f })
      toast(d.first_tap ? '已记录反馈，该桶首次开桶' : '反馈已同步给品控')
      if (d.warning) toast(d.warning, 'err')
      onDone()
    } catch (e) { toast(e.message, 'err') }
  }
  return (
    <Modal title={`开桶反馈 · ${c.keg_code}（${c.beer}）`} onClose={onClose}>
      <div className="grid cols-2">
        <div><label>出酒速度</label>
          <select value={f.pour_speed} onChange={e => set('pour_speed', e.target.value)}>
            <option>正常</option><option>偏慢</option><option>过快</option><option>不出酒</option>
          </select></div>
        <div><label>气压</label>
          <select value={f.pressure} onChange={e => set('pressure', e.target.value)}>
            <option>正常</option><option>气压过高</option><option>气压过低</option><option>波动</option>
          </select></div>
      </div>
      <div className="checks" style={{ marginTop: 12 }}>
        <label><input type="checkbox" checked={f.taste_off} onChange={e => set('taste_off', e.target.checked)} /> 口味异常（酸/纸板味/异味等）</label>
      </div>
      {f.taste_off && <div style={{ marginTop: 6 }}><label>口味描述</label>
        <input value={f.taste_note} onChange={e => set('taste_note', e.target.value)} placeholder="如：入口发酸，后段有纸板味" /></div>}
      <div style={{ marginTop: 6 }}><label>客诉记录</label>
        <textarea rows={2} value={f.complaint} onChange={e => set('complaint', e.target.value)} placeholder="客人反馈/退酒情况" /></div>
      <div className="row end" style={{ marginTop: 16 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button onClick={submit}>提交反馈</button>
      </div>
    </Modal>
  )
}

function ReserveModal({ ids, onClose, onDone }) {
  const [date, setDate] = useState('2026-09-28')
  const [note, setNote] = useState('')
  const toast = useToast()
  const submit = async () => {
    try {
      const d = await api('/cycles/reserve', { method: 'POST', body: { cycle_ids: ids, pickup_date: date, note } })
      toast(`预约成功（单号 ${d.reservation_id}），等待司机上门`)
      onDone()
    } catch (e) { toast(e.message, 'err') }
  }
  return (
    <Modal title={`预约空桶回收 · ${ids.length} 只`} onClose={onClose}>
      <label>期望取桶日期</label>
      <input type="date" value={date} onChange={e => setDate(e.target.value)} />
      <label>给司机的备注（停车/入口等）</label>
      <input value={note} onChange={e => setNote(e.target.value)} />
      <div className="row end" style={{ marginTop: 16 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button onClick={submit}>确认预约</button>
      </div>
    </Modal>
  )
}
