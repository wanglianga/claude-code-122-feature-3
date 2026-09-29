import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api.js'
import { Stat, KEG_STATUS as KEG_T, STAGE as STAGE_T, BATCH_STATUS as BATCH_T } from '../components.jsx'

export default function Overview() {
  const [d, setD] = useState(null)
  useEffect(() => { api('/overview').then(setD) }, [])
  if (!d) return <div className="muted">加载中…</div>

  const kb = d.keg_by_status || {}
  const cb = d.cycle_by_stage || {}
  const bb = d.batch_by_status || {}

  return (
    <div>
      <h1>运营总览</h1>
      <div className="grid cols-4">
        <Stat label="酒桶资产总数" value={d.keg_total} sub={`在库 ${kb.IN_STOCK || 0} · 在店 ${kb.AT_BAR || 0}`} />
        <Stat label="占用中押金" value={`¥${d.deposit_held}`} kind="warn" sub="在流转桶押金" />
        <Stat label="已释放押金" value={`¥${d.deposit_released}`} kind="ok" />
        <Stat label="累计扣款" value={`¥${d.deposit_deducted}`} kind="alert" sub="丢失/损坏/清洗不合格" />
      </div>

      <h2 style={{ margin: '18px 0 10px' }}>需要处理的预警</h2>
      <div className="grid cols-4">
        <Stat label="账期逾期未闭环" value={d.overdue} kind={d.overdue ? 'alert' : 'ok'}
          sub={<Link to="/finance?overdue=1">去财务处理 →</Link>} />
        <Stat label="司机漏扫待核查" value={d.missing_scan} kind={d.missing_scan ? 'alert' : 'ok'}
          sub={<Link to="/driver">去回收任务核对 →</Link>} />
        <Stat label="清洗不合格桶" value={d.wash_fail} kind={d.wash_fail ? 'alert' : 'ok'}
          sub={<Link to="/rework">不合格返修 →</Link>} />
        <Stat label="返修中（待复检）" value={d.rework_open || 0} kind={d.rework_open ? 'alert' : 'ok'}
          sub={<Link to="/rework">清洗厂处理 →</Link>} />
        <Stat label="返修待仓库确认" value={d.rework_pending_confirm || 0} kind={d.rework_pending_confirm ? 'warn' : 'ok'}
          sub={<Link to="/rework">确认恢复灌装 →</Link>} />
        <Stat label="返修账单待确认" value={d.rework_bill_pending || 0} kind={d.rework_bill_pending ? 'warn' : 'ok'}
          sub={<Link to="/rework">财务入账 →</Link>} />
        <Stat label="丢失酒桶" value={d.lost} kind={d.lost ? 'alert' : 'ok'}
          sub={<Link to="/finance">押金扣款 →</Link>} />
        <Stat label="桶体变形" value={d.deformed} kind={d.deformed ? 'warn' : 'ok'} />
        <Stat label="多店口味投诉批次" value={d.taste_alerts} kind={d.taste_alerts ? 'alert' : 'ok'}
          sub={<Link to="/qc">查看停售建议 →</Link>} />
        <Stat label="待取回收预约" value={d.waiting_pickups} kind={d.waiting_pickups ? 'warn' : 'ok'}
          sub={<Link to="/driver">司机任务 →</Link>} />
        <Stat label="待配送订单" value={d.pending_orders}
          sub={<Link to="/orders">安排发货 →</Link>} />
      </div>

      <div className="grid cols-3" style={{ marginTop: 18 }}>
        <div className="panel">
          <h3>酒桶资产状态</h3>
          <StateDist data={kb} map={KEG_T} />
        </div>
        <div className="panel">
          <h3>桶流转阶段</h3>
          <StateDist data={cb} map={STAGE_T} />
        </div>
        <div className="panel">
          <h3>批次状态</h3>
          <StateDist data={bb} map={BATCH_T} />
        </div>
      </div>
    </div>
  )
}

const BAR_COLOR = { green: '#3e7d4c', blue: '#34607d', amber: '#d0892a', red: '#c0392b', purple: '#7a4a9a', gray: '#9b9486' }

function StateDist({ data, map }) {
  const entries = Object.entries(data)
  const total = entries.reduce((s, [, v]) => s + Number(v), 0)
  if (!total) return <div className="muted small">无数据</div>
  return (
    <div>
      {entries.map(([k, v]) => {
        const [text, cls] = map[k] || [k, 'gray']
        const pct = Math.round(Number(v) / total * 100)
        return (
          <div key={k} style={{ marginBottom: 8 }}>
            <div className="row small" style={{ justifyContent: 'space-between' }}>
              <span><span className={'badge ' + cls}>{text}</span></span>
              <span className="muted">{v} 只 · {pct}%</span>
            </div>
            <div style={{ height: 6, background: '#eee7d8', borderRadius: 4, marginTop: 3 }}>
              <div style={{ height: '100%', width: pct + '%', borderRadius: 4, background: BAR_COLOR[cls] }} />
            </div>
          </div>
        )
      })}
    </div>
  )
}
