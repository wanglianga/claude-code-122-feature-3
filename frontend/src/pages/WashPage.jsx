import React, { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api.js'
import { useToast, Empty } from '../components.jsx'

export default function WashPage() {
  const [list, setList] = useState([])
  const [check, setCheck] = useState(null)
  const toast = useToast()
  const load = () => api('/wash/pending').then(d => setList(d.pending))
  useEffect(load, [])

  return (
    <div>
      <h1>清洗厂检测</h1>
      <div className="muted small" style={{ marginBottom: 12 }}>
        对司机收回的空桶做耐压、密封、内壁清洁检测；合格桶回库恢复再灌装资格，不合格桶冻结并通知品控/财务。
      </div>
      <div className="suggest danger" style={{ marginBottom: 12, display: 'flex', alignItems: 'center', gap: 12 }}>
        <span>发现桶内异味 / 阀门损坏等清洗不合格情况？检测后请到「不合格返修」发起返修单并上传桶体照片。</span>
        <Link to="/rework" className="small" style={{
          whiteSpace: 'nowrap', display: 'inline-block', padding: '3px 9px', borderRadius: 5,
          background: 'var(--danger)', color: '#fff', textDecoration: 'none'
        }}>去发起返修 →</Link>
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
  const [note, setNote] = useState('')
  const [inspector, setInspector] = useState('刘师傅')
  const toast = useToast()
  const passed = pressure && seal && clean

  const submit = async () => {
    try {
      const d = await api('/wash/checks', {
        method: 'POST',
        body: { cycle_id: c.cycle_id, inspector, passed, pressure_ok: pressure, seal_ok: seal, cleanliness_ok: clean, defect_note: note }
      })
      toast(d.passed ? '检测合格，桶已回库恢复再灌装' : '检测不合格：桶已冻结，取消再灌装资格')
      onDone()
    } catch (e) { toast(e.message, 'err') }
  }

  return (
    <div className="modal-mask" onMouseDown={e => e.target === e.currentTarget && onClose()}>
      <div className="modal">
        <h2>清洗检测 · {c.keg_code}</h2>
        <div className="muted small">{c.beer}（{c.style}）{c.batch_no} · 来自 {c.bar}</div>
        <div className="checks" style={{ marginTop: 14 }}>
          <label><input type="checkbox" checked={pressure} onChange={e => setPressure(e.target.checked)} /> 耐压检测合格</label>
          <label><input type="checkbox" checked={seal} onChange={e => setSeal(e.target.checked)} /> 密封圈完好</label>
          <label><input type="checkbox" checked={clean} onChange={e => setClean(e.target.checked)} /> 内壁清洁合格</label>
        </div>
        <label>检测员</label><input value={inspector} onChange={e => setInspector(e.target.value)} />
        <label>缺陷说明（不合格时必填建议）</label>
        <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
          placeholder="如：桶身变形、密封圈损坏、内壁残留明显" />
        <div className={'suggest ' + (passed ? '' : 'danger')} style={{ marginTop: 12 }}>
          {passed ? '判定：检测合格 → 回库，可再次灌装' : '判定：检测不合格 → WASH_FAIL，取消再灌装资格，财务据记录扣款'}
        </div>
        <div className="row end" style={{ marginTop: 14 }}>
          <button className="subtle" onClick={onClose}>取消</button>
          <button className={passed ? '' : 'danger'} onClick={submit}>提交检测</button>
        </div>
      </div>
    </div>
  )
}
