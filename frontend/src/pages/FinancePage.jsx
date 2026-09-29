import React, { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api } from '../api.js'
import { Badge, DEPOSIT, STAGE, CHARGE_TYPE, CHARGE_STATUS, PARTY_TYPE, useToast, Empty, Stat, Money } from '../components.jsx'
import { RepairDetailModal } from './repair-ui.jsx'

export default function FinancePage() {
  const [tab, setTab] = useState('deposit')
  return (
    <div>
      <h1>财务结算</h1>
      <div className="tabs">
        <button className={'tabbtn' + (tab === 'deposit' ? ' active' : '')} onClick={() => setTab('deposit')}>押金结算</button>
        <button className={'tabbtn' + (tab === 'billing' ? ' active' : '')} onClick={() => setTab('billing')}>返修账单</button>
      </div>
      {tab === 'deposit' ? <Deposits /> : <Billing />}
    </div>
  )
}

// ---------- 押金 ----------
function Deposits() {
  const [sp] = useSearchParams()
  const [filter, setFilter] = useState(sp.get('overdue') ? 'overdue' : '')
  const [data, setData] = useState(null)
  const toast = useToast()
  const load = () => {
    const q = filter === 'overdue' ? '?overdue=1' : filter ? '?status=' + filter : ''
    api('/finance/deposits' + q).then(setData)
  }
  useEffect(load, [filter])

  const settle = async (c, action) => {
    let deduct = 0
    let reason = ''
    if (action === 'partial') {
      const v = prompt(`对 ${c.keg_code} 部分扣款金额（押金 ¥${c.deposit_amount}）：`)
      if (!v) return
      deduct = Number(v)
      reason = prompt('扣款原因：') || ''
    } else if (action === 'deduct') {
      reason = prompt('扣款原因：', '酒桶丢失/严重损坏，押金全额扣除') || ''
    }
    try {
      const d = await api('/finance/settle/' + c.cycle_id, { method: 'POST', body: { action, deduct_amount: deduct, reason } })
      toast(`结算完成：${d.deposit_status} 释放 ¥${d.released_amount} 扣款 ¥${d.deducted_amount}`)
      load()
    } catch (e) { toast(e.message, 'err') }
  }

  if (!data) return <div className="muted">加载中…</div>
  const s = data.summary

  return (
    <div>
      <div className="grid cols-4" style={{ marginTop: 12 }}>
        <Stat label="占用中押金" value={<Money v={s.held_amount} />} kind="warn" sub={`${s.held_count} 只桶`} />
        <Stat label="其中逾期未闭环" value={s.overdue_count} kind={s.overdue_count ? 'alert' : 'ok'} />
        <Stat label="累计释放押金" value={<Money v={s.released_total} />} kind="ok" />
        <Stat label="累计扣款押金" value={<Money v={s.deducted_total} />} kind="alert" />
      </div>

      <div className="toolbar" style={{ marginTop: 16 }}>
        <div className="f"><label>筛选</label>
          <select value={filter} onChange={e => setFilter(e.target.value)}>
            <option value="">全部周期</option>
            <option value="overdue">仅账期逾期</option>
            <option value="HELD">押金占用中</option>
            <option value="RELEASED">已释放</option>
            <option value="DEDUCTED">已扣款</option>
            <option value="PARTIAL">部分扣款</option>
          </select></div>
      </div>

      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>桶编号</th><th>酒吧/城市</th><th>酒款/批次</th><th>账期</th>
            <th>押金</th><th>释放/扣款</th><th>桶况与异常</th><th>状态</th><th>按桶操作</th>
          </tr></thead>
          <tbody>
            {data.deposits.map(c => (
              <tr key={c.cycle_id}>
                <td><code className="kcode">{c.keg_code}</code></td>
                <td>{c.bar}<div className="small muted">{c.city}</div></td>
                <td className="small">{c.style} {c.batch_no}</td>
                <td className="small">{c.deposit_due_date}
                  {c.overdue_days > 0 && c.deposit_status === 'HELD' &&
                    <div style={{ color: 'var(--danger)' }}>逾期 {c.overdue_days} 天</div>}
                </td>
                <td><Money v={c.deposit_amount} /></td>
                <td className="small">
                  {c.deposit_status === 'HELD' ? '—'
                    : <>释放 <b>¥{c.released_amount}</b><br />扣款 <b style={{ color: 'var(--danger)' }}>¥{c.deducted_amount}</b>
                      {c.deduct_reason && <div className="muted">{c.deduct_reason}</div>}
                    </>}
                </td>
                <td className="small">
                  {[c.late_return && '延迟归还', c.missing_scan && '司机漏扫', c.lost && '丢失', c.deformed && '变形',
                    (c.residue_level !== '无' && c.residue_level) && `残液:${c.residue_level}`,
                    (c.cap_status !== '已封盖' && c.cap_status) && c.cap_status]
                    .filter(Boolean).map((x, i) => <span key={i} className="badge red" style={{ margin: 2 }}>{x}</span>)}
                  {!c.late_return && !c.missing_scan && !c.lost && !c.deformed && c.residue_level === '无' && c.cap_status === '已封盖'
                    && <span className="muted">桶况完好</span>}
                </td>
                <td><Badge map={DEPOSIT} value={c.deposit_status} /><div className="small muted"><Badge map={STAGE} value={c.stage} /></div></td>
                <td>
                  {c.deposit_status === 'HELD'
                    ? <div className="flags" style={{ flexDirection: 'column', alignItems: 'flex-start' }}>
                        <button className="small green" style={{ background: 'var(--ok)', borderColor: 'var(--ok)' }}
                          onClick={() => settle(c, 'release')}>释放押金</button>
                        <button className="small subtle" onClick={() => settle(c, 'partial')}>部分扣款</button>
                        <button className="small danger" onClick={() => settle(c, 'deduct')}>全额扣款</button>
                      </div>
                    : <span className="muted small">{c.settled_at ? `已结算 ${c.settled_at}` : '已结算'}</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data.deposits.length === 0 && <Empty text="无符合条件的记录" />}
      </div>
    </div>
  )
}

// ---------- 返修账单 ----------
function Billing() {
  const [data, setData] = useState(null)
  const [status, setStatus] = useState('PENDING')
  const [partyType, setPartyType] = useState('')
  const [detail, setDetail] = useState(null)
  const toast = useToast()
  const load = () => {
    const p = new URLSearchParams()
    if (status) p.set('status', status)
    if (partyType) p.set('party_type', partyType)
    api('/finance/billing?' + p.toString()).then(setData)
  }
  useEffect(load, [status, partyType])

  const confirm = async (c) => {
    if (!window.confirm(`确认 ${c.repair_no}（${c.keg_code}）的${CHARGE_TYPE[c.charge_type]} ¥${c.amount} 入账到 ${PARTY_TYPE[c.party_type]}「${c.party_name}」？`)) return
    try {
      await api('/finance/billing/confirm/' + c.id, { method: 'POST' })
      toast('账单已确认并同步到对应酒吧/清洗厂账单')
      load()
    } catch (e) { toast(e.message, 'err') }
  }

  if (!data) return <div className="muted">加载中…</div>
  const s = data.summary

  return (
    <div>
      <div className="grid cols-4" style={{ marginTop: 12 }}>
        <Stat label="待确认账单金额" value={<Money v={s.pending_amount} />} kind="warn" sub={`${s.pending_count} 条`} />
        <Stat label="已确认入账金额" value={<Money v={s.confirmed_amount} />} kind="ok" sub={`${s.confirmed_count} 条`} />
      </div>

      <div className="panel" style={{ marginTop: 16 }}>
        <h3 style={{ marginTop: 0 }}>按承担方汇总</h3>
        <table className="minitable">
          <thead><tr><th>承担方类型</th><th>名称</th><th>待确认</th><th>已确认</th></tr></thead>
          <tbody>
            {data.by_party.map((p, i) => (
              <tr key={i}>
                <td>{PARTY_TYPE[p.party_type]}</td><td>{p.party_name}</td>
                <td style={{ color: p.pending_amount > 0 ? 'var(--warn)' : undefined }}>¥{p.pending_amount}</td>
                <td style={{ color: 'var(--ok)' }}>¥{p.confirmed_amount}</td>
              </tr>
            ))}
            {data.by_party.length === 0 && <tr><td colSpan={4} className="muted small">暂无账单</td></tr>}
          </tbody>
        </table>
      </div>

      <div className="toolbar" style={{ marginTop: 16 }}>
        <div className="f"><label>确认状态</label>
          <select value={status} onChange={e => setStatus(e.target.value)}>
            <option value="PENDING">待财务确认</option>
            <option value="CONFIRMED">已确认</option>
            <option value="">全部</option>
          </select></div>
        <div className="f"><label>承担方</label>
          <select value={partyType} onChange={e => setPartyType(e.target.value)}>
            <option value="">全部</option>
            <option value="BAR">酒吧</option>
            <option value="WASHPLANT">清洗厂</option>
          </select></div>
        <button className="subtle" onClick={load}>刷新</button>
      </div>

      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>账单</th><th>桶/返修单</th><th>类型</th><th>承担方</th><th>金额</th><th>摘要</th><th>状态</th><th>操作</th>
          </tr></thead>
          <tbody>
            {data.charges.map(c => (
              <tr key={c.id}>
                <td className="small muted">#{c.id}<div>{c.created_at}</div></td>
                <td><code className="kcode">{c.keg_code}</code><div className="small">{c.repair_no}</div></td>
                <td>{CHARGE_TYPE[c.charge_type]}</td>
                <td className="small">{PARTY_TYPE[c.party_type]}<div className="muted">{c.party_name}</div></td>
                <td><Money v={c.amount} /></td>
                <td className="small muted">{c.memo}</td>
                <td><Badge map={CHARGE_STATUS} value={c.status} />
                  {c.confirmed_at && <div className="small muted">{c.confirmed_at}</div>}</td>
                <td>
                  {c.status === 'PENDING'
                    ? <button className="small" onClick={() => confirm(c)}>确认入账</button>
                    : <button className="small ghost" onClick={() => setDetail(c.repair_id)}>返修详情</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data.charges.length === 0 && <Empty text="无符合条件的账单" />}
      </div>

      {detail && <RepairDetailModal id={detail} onClose={() => { setDetail(null); load() }} />}
    </div>
  )
}
