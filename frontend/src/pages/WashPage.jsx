import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { Badge, REPAIR_STATUS, Money, Empty, useToast } from '../components.jsx'
import { RepairCreateModal, RecheckModal, RepairDetailModal } from './repair-ui.jsx'

export default function WashPage() {
  const [tab, setTab] = useState('wash')
  return (
    <div>
      <h1>清洗厂检测与返修</h1>
      <div className="tabs">
        <button className={'tabbtn' + (tab === 'wash' ? ' active' : '')} onClick={() => setTab('wash')}>待清洗检测</button>
        <button className={'tabbtn' + (tab === 'repair' ? ' active' : '')} onClick={() => setTab('repair')}>不合格返修</button>
      </div>
      {tab === 'wash' ? <WashChecks /> : <Repairs />}
    </div>
  )
}

// ---------- 待清洗检测 ----------
function WashChecks() {
  const [list, setList] = useState([])
  const [check, setCheck] = useState(null)
  const toast = useToast()
  const load = () => api('/wash/pending').then(d => setList(d.pending))
  useEffect(load, [])

  return (
    <div>
      <div className="muted small" style={{ margin: '10px 0 12px' }}>
        对司机收回的空桶登记清洗批次并做耐压、密封、内壁清洁检测；合格桶回库恢复再灌装资格，不合格桶冻结后可发起返修。
      </div>
      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>桶编号</th><th>酒款/批次</th><th>来源酒吧</th><th>回收司机登记</th><th>收回时间</th><th>操作</th>
          </tr></thead>
          <tbody>
            {list.map(c => (
              <tr key={c.cycle_id}>
                <td><code className="kcode">{c.keg_code}</code><div className="small muted">{c.capacity_liters}L</div></td>
                <td>{c.beer}（{c.style}）<div className="small muted">{c.batch_no}</div></td>
                <td>{c.bar || '—'}</td>
                <td className="small">
                  外观：{c.appearance} · 残液：{c.residue_level}<br />封盖：{c.cap_status}
                </td>
                <td className="small muted">{c.picked_at}</td>
                <td><button className="small" onClick={() => setCheck(c)}>登记检测结果</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {list.length === 0 && <Empty text="暂无待清洗检测的空桶" />}
      </div>
      {check && <CheckModal c={check} onClose={() => setCheck(null)} onDone={() => { setCheck(null); load() }} />}
    </div>
  )
}

function CheckModal({ c, onClose, onDone }) {
  const [pressure, setPressure] = useState(true)
  const [seal, setSeal] = useState(true)
  const [clean, setClean] = useState(true)
  const [odor, setOdor] = useState(false) // 快捷：发现异味 → 取消内壁合格
  const [note, setNote] = useState('')
  const [inspector, setInspector] = useState('刘师傅')
  const [batchNo, setBatchNo] = useState('')
  const toast = useToast()
  const passed = pressure && seal && clean

  const submit = async () => {
    try {
      const d = await api('/wash/checks', {
        method: 'POST',
        body: { cycle_id: c.cycle_id, inspector, passed, pressure_ok: pressure, seal_ok: seal, cleanliness_ok: clean, defect_note: note, wash_batch_no: batchNo }
      })
      if (d.passed) {
        toast('检测合格，桶已回库恢复再灌装')
      } else {
        toast('检测不合格：桶已冻结，请到「不合格返修」发起返修', 'err')
      }
      onDone()
    } catch (e) { toast(e.message, 'err') }
  }

  return (
    <div className="modal-mask" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="modal">
        <h2>清洗检测 · {c.keg_code}</h2>
        <div className="muted small">{c.beer}（{c.style}）{c.batch_no} · 来自 {c.bar}</div>
        <div className="grid cols-2" style={{ marginTop: 10 }}>
          <div><label>清洗批次号（留空自动生成）</label>
            <input value={batchNo} onChange={e => setBatchNo(e.target.value)} placeholder="如 WB-20260929-SH-A" /></div>
          <div><label>检测员</label><input value={inspector} onChange={e => setInspector(e.target.value)} /></div>
        </div>
        <div className="checks" style={{ marginTop: 12 }}>
          <label><input type="checkbox" checked={pressure} onChange={e => setPressure(e.target.checked)} /> 耐压检测合格</label>
          <label><input type="checkbox" checked={seal} onChange={e => setSeal(e.target.checked)} /> 密封圈完好</label>
          <label><input type="checkbox" checked={clean} onChange={e => setClean(e.target.checked)} /> 内壁清洁合格</label>
          <label><input type="checkbox" checked={odor} onChange={e => { setOdor(e.target.checked); if (e.target.checked) setClean(false) }} /> 发现桶内异味</label>
        </div>
        <label style={{ marginTop: 10 }}>缺陷说明（不合格时必填建议）</label>
        <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
          placeholder="如：桶内异味（酸臭）、阀门损坏、密封圈损坏、内壁残留明显" />
        <div className={'suggest ' + (passed ? '' : 'danger')} style={{ marginTop: 12 }}>
          {passed ? '判定：检测合格 → 回库，可再次灌装' : '判定：检测不合格 → 冻结桶体、暂停再灌装，随后发起返修单并上传桶体照片'}
        </div>
        <div className="row end" style={{ marginTop: 14 }}>
          <button className="subtle" onClick={onClose}>取消</button>
          <button className={passed ? '' : 'danger'} onClick={submit}>提交检测</button>
        </div>
      </div>
    </div>
  )
}

// ---------- 不合格返修 ----------
function Repairs() {
  const [data, setData] = useState(null)
  const [filter, setFilter] = useState('')
  const [creating, setCreating] = useState(null)
  const [rechecking, setRechecking] = useState(null)
  const [detail, setDetail] = useState(null)
  const load = () => {
    const q = filter ? '?status=' + filter : ''
    api('/repairs' + q).then(setData)
  }
  useEffect(load, [filter])

  if (!data) return <div className="muted">加载中…</div>

  return (
    <div>
      <div className="muted small" style={{ margin: '10px 0 12px' }}>
        检测发现桶内异味或阀门损坏等缺陷时发起返修：平台自动追溯上一家酒吧、回收司机与清洗批次，同步生成返修费、押金扣减与暂停再灌装状态；返修完成后上传复检结果与照片，酒厂仓库确认后桶才恢复可灌装。
      </div>

      <h2 style={{ margin: '6px 0 8px' }}>待返修的不合格桶（{data.can_create.length}）</h2>
      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>桶编号</th><th>酒款/批次</th><th>上一家酒吧/回收司机</th><th>清洗批次/检测缺陷</th><th>操作</th>
          </tr></thead>
          <tbody>
            {data.can_create.map(c => (
              <tr key={c.cycle_id}>
                <td><code className="kcode">{c.keg_code}</code><div className="small muted">{c.capacity_liters}L</div></td>
                <td className="small">{c.beer}（{c.style}）<br />{c.batch_no}</td>
                <td className="small">{c.bar}<br /><span className="muted">{c.driver}</span></td>
                <td className="small">{c.wash_batch_no}<div className="muted">{c.defect_note}</div></td>
                <td><button className="small danger" onClick={() => setCreating(c)}>发起返修</button></td>
              </tr>
            ))}
          </tbody>
        </table>
        {data.can_create.length === 0 && <Empty text="暂无待发起返修的不合格桶" />}
      </div>

      <div className="toolbar" style={{ marginTop: 18 }}>
        <h2 style={{ margin: 0 }}>返修单</h2>
        <span className="spacer" />
        <div className="f"><label>状态</label>
          <select value={filter} onChange={e => setFilter(e.target.value)}>
            <option value="">全部</option>
            <option value="OPEN">返修中</option>
            <option value="SUBMITTED">待仓库确认</option>
            <option value="CONFIRMED">已确认回库</option>
          </select></div>
        <button className="subtle" onClick={load}>刷新</button>
      </div>
      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>返修单/桶</th><th>缺陷</th><th>追溯</th><th>费用/押金扣减</th><th>状态</th><th>操作</th>
          </tr></thead>
          <tbody>
            {data.repairs.map(r => (
              <tr key={r.id}>
                <td><b>{r.repair_no}</b><br /><code className="kcode">{r.keg_code}</code></td>
                <td className="small">{r.defect_type}<div className="muted">{r.defect_note}</div></td>
                <td className="small">{r.bar}<div className="muted">{r.driver} · {r.wash_batch_no}</div></td>
                <td className="small">返修费 <Money v={r.repair_fee} /><div className="muted">押金扣减 ¥{r.deposit_deduct}</div></td>
                <td><Badge map={REPAIR_STATUS} value={r.status} />
                  {r.pending_charge_count > 0 && <div className="badge amber" style={{ marginTop: 4 }}>账单待确认 ×{r.pending_charge_count}</div>}
                </td>
                <td>
                  <button className="small ghost" onClick={() => setDetail(r.id)}>详情/照片</button>
                  {r.status === 'OPEN' && <button className="small" style={{ marginLeft: 6 }} onClick={() => setRechecking(r)}>返修完成上传</button>}
                  {r.status === 'SUBMITTED' && <span className="small muted">已提交，待仓库确认</span>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data.repairs.length === 0 && <Empty text="暂无返修单" />}
      </div>

      {creating && <RepairCreateModal cycle={creating} onClose={() => setCreating(null)} onDone={() => { setCreating(null); load() }} />}
      {rechecking && <RecheckModal repair={rechecking} onClose={() => setRechecking(null)} onDone={() => { setRechecking(null); load() }} />}
      {detail && <RepairDetailModal id={detail} onClose={() => setDetail(null)} />}
    </div>
  )
}
