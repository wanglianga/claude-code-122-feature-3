import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { Badge, REPAIR_STATUS, CHARGE_TYPE, CHARGE_STATUS, PARTY_TYPE, Money, Modal, PhotoUploader, PhotoGallery, useToast } from '../components.jsx'

// 返修详情（含追溯链路 / 照片 / 账单）
export function RepairDetailModal({ id, onClose, extra }) {
  const [d, setD] = useState(null)
  const load = () => api('/repairs/' + id).then(setD)
  useEffect(load, [id])
  return (
    <Modal wide title={d ? `返修单 ${d.repair.repair_no} · ${d.repair.keg_code}` : '返修详情'} onClose={onClose}>
      {!d ? <div className="muted">加载中…</div> : (
        <div>
          <div className="grid cols-2">
            <div>
              <h3>缺陷与费用</h3>
              <dl className="kv">
                <dt>缺陷类型</dt><dd>{d.repair.defect_type}{d.repair.defect_note ? `：${d.repair.defect_note}` : ''}</dd>
                <dt>返修状态</dt><dd><Badge map={REPAIR_STATUS} value={d.repair.status} /></dd>
                <dt>返修费用</dt><dd><Money v={d.repair.repair_fee} />（承担方：{PARTY_TYPE[d.repair.fee_party]}）</dd>
                <dt>押金扣减</dt><dd><Money v={d.repair.deposit_deduct} /></dd>
                <dt>清洗批次</dt><dd>{d.repair.wash_batch_no || '—'}</dd>
                {d.repair.recheck_result && <>
                  <dt>清洗厂复检</dt><dd>{d.repair.recheck_result}（{d.repair.recheck_inspector || '—'}）{d.repair.recheck_note ? `：${d.repair.recheck_note}` : ''}</dd>
                </>}
                {d.repair.confirm_note && <><dt>仓库确认意见</dt><dd>{d.repair.confirm_note}</dd></>}
              </dl>
              <h3>平台追溯</h3>
              <TraceBox trace={d.trace} />
            </div>
            <div>
              <h3>桶体照片（仓库留档）</h3>
              <PhotoGallery photos={d.photos} />
              <h3 style={{ marginTop: 16 }}>账单条目</h3>
              <table className="minitable">
                <thead><tr><th>类型</th><th>承担方</th><th>金额</th><th>状态</th></tr></thead>
                <tbody>
                  {d.charges.map(c => (
                    <tr key={c.id}>
                      <td>{CHARGE_TYPE[c.charge_type]}</td>
                      <td className="small">{PARTY_TYPE[c.party_type]} · {c.party_name}</td>
                      <td><Money v={c.amount} /></td>
                      <td><Badge map={CHARGE_STATUS} value={c.status} /></td>
                    </tr>
                  ))}
                  {d.charges.length === 0 && <tr><td colSpan={4} className="muted small">无</td></tr>}
                </tbody>
              </table>
              {extra && <div style={{ marginTop: 16 }}>{extra(d, load)}</div>}
            </div>
          </div>
        </div>
      )}
    </Modal>
  )
}

export function TraceBox({ trace }) {
  if (!trace) return <span className="muted small">—</span>
  const wc = trace.wash_check || {}
  const ps = trace.pickup_scan || {}
  return (
    <dl className="kv small">
      <dt>上一家酒吧</dt><dd>{trace.bar ? `${trace.bar.name}（${trace.bar.manager || '—'} ${trace.bar.phone || ''}）` : '—'}</dd>
      <dt>回收司机</dt><dd>{trace.driver ? `${trace.driver.name} ${trace.driver.phone || ''}` : '—'}</dd>
      <dt>酒液批次</dt><dd>{trace.batch ? `${trace.batch.batch_no} · ${trace.batch.beer}（${trace.batch.style}）` : '—'}</dd>
      <dt>清洗厂/城市</dt><dd>{trace.washplant ? `${trace.washplant.name} · ${trace.washplant.city}` : '—'}</dd>
      <dt>清洗批次号</dt><dd>{wc.wash_batch_no || '—'}</dd>
      <dt>司机回收登记</dt><dd>{ps.scanned_at ? `外观 ${ps.appearance} · 残液 ${ps.residue_level} · 封盖 ${ps.cap_status}（${ps.scanned_at}）` : '—'}</dd>
      <dt>检测记录</dt><dd>{wc.checked_at ? `${wc.passed ? '合格' : '不合格'} · 耐压${wc.pressure_ok ? '✓' : '✗'} 密封${wc.seal_ok ? '✓' : '✗'} 内壁${wc.cleanliness_ok ? '✓' : '✗'}（${wc.checked_at}）` : '—'}</dd>
    </dl>
  )
}

// 清洗厂发起返修
export function RepairCreateModal({ cycle, onClose, onDone }) {
  const [defectType, setType] = useState('异味')
  const [note, setNote] = useState(cycle.defect_note || '')
  const [repairFee, setFee] = useState(80)
  const [depositDeduct, setDeduct] = useState(100)
  const [feeParty, setFeeParty] = useState('BAR')
  const [photos, setPhotos] = useState([])
  const [busy, setBusy] = useState(false)
  const toast = useToast()

  const submit = async () => {
    if (photos.length === 0) { toast('必须上传至少 1 张桶体缺陷照片', 'err'); return }
    setBusy(true)
    try {
      const d = await api('/repairs/create', {
        method: 'POST',
        body: {
          cycle_id: cycle.cycle_id, defect_type: defectType, defect_note: note,
          repair_fee: Number(repairFee) || 0, deposit_deduct: Number(depositDeduct) || 0,
          fee_party: feeParty, wash_batch_no: cycle.wash_batch_no || '',
          photos: photos.map(p => ({ name: p.name, content_type: p.content_type, data: p.data })),
        },
      })
      toast(`返修单 ${d.repair_no} 已创建：桶暂停再灌装，费用与押金扣减账单已同步生成`)
      onDone()
    } catch (e) { toast(e.message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={`发起返修 · ${cycle.keg_code}`} onClose={onClose}>
      <div className="muted small" style={{ marginBottom: 10 }}>
        {cycle.beer}（{cycle.style}）{cycle.batch_no} · 上一家酒吧 <b>{cycle.bar || '—'}</b> · 回收司机 <b>{cycle.driver || '—'}</b> · 清洗批次 <b>{cycle.wash_batch_no || '—'}</b>
      </div>
      <div className="grid cols-2">
        <div><label>缺陷类型</label>
          <select value={defectType} onChange={e => setType(e.target.value)}>
            <option>异味</option><option>阀门损坏</option><option>变形</option><option>其他</option>
          </select></div>
        <div><label>费用承担方</label>
          <select value={feeParty} onChange={e => setFeeParty(e.target.value)}>
            <option value="BAR">上一家酒吧</option>
            <option value="WASHPLANT">清洗厂（清洗责任）</option>
          </select></div>
        <div><label>返修费用 ¥</label>
          <input type="number" min="0" value={repairFee} onChange={e => setFee(e.target.value)} /></div>
        <div><label>押金扣减 ¥</label>
          <input type="number" min="0" value={depositDeduct} onChange={e => setDeduct(e.target.value)} /></div>
      </div>
      <label>缺陷说明</label>
      <textarea rows={2} value={note} onChange={e => setNote(e.target.value)}
        placeholder="如：开桶桶内酸臭异味 / 阀门损坏无法保压" />
      <div style={{ marginTop: 8 }}><PhotoUploader photos={photos} setPhotos={setPhotos} /></div>
      <div className="suggest" style={{ marginTop: 12 }}>
        提交后：桶体进入「返修中」并暂停再灌装；返修费与押金扣减同步生成账单（待财务确认），系统通知上一家酒吧。
      </div>
      <div className="row end" style={{ marginTop: 14 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button className="danger" disabled={busy} onClick={submit}>确认发起返修</button>
      </div>
    </Modal>
  )
}

// 清洗厂返修完成：上传复检结果与照片
export function RecheckModal({ repair, onClose, onDone }) {
  const [result, setResult] = useState('合格')
  const [inspector, setInspector] = useState('刘师傅')
  const [note, setNote] = useState('')
  const [photos, setPhotos] = useState([])
  const [busy, setBusy] = useState(false)
  const toast = useToast()

  const submit = async () => {
    if (photos.length === 0) { toast('请上传至少 1 张复检桶体照片', 'err'); return }
    setBusy(true)
    try {
      await api('/repairs/submit/' + repair.id, {
        method: 'POST',
        body: {
          recheck_result: result, inspector, note,
          photos: photos.map(p => ({ name: p.name, content_type: p.content_type, data: p.data })),
        },
      })
      toast('复检结果已上传，等待酒厂仓库确认后桶才恢复可灌装')
      onDone()
    } catch (e) { toast(e.message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={`返修完成复检 · ${repair.keg_code}（${repair.repair_no}）`} onClose={onClose}>
      <div className="grid cols-2">
        <div><label>复检结论</label>
          <select value={result} onChange={e => setResult(e.target.value)}>
            <option>合格</option><option>不合格</option>
          </select></div>
        <div><label>检测员</label><input value={inspector} onChange={e => setInspector(e.target.value)} /></div>
      </div>
      <label>返修/复检说明</label>
      <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
        placeholder="如：深度酸洗+蒸汽灭菌两轮，异味消除，耐压/密封/内壁全部合格" />
      <div style={{ marginTop: 8 }}><PhotoUploader photos={photos} setPhotos={setPhotos} hint="复检照片随返修单由仓库长期保留" /></div>
      <div className={'suggest ' + (result === '合格' ? '' : 'danger')} style={{ marginTop: 12 }}>
        {result === '合格'
          ? '判定合格 → 提交后由酒厂仓库复检确认，确认后桶回库并恢复再灌装资格。'
          : '判定仍不合格 → 提交后由仓库知悉，可继续返修。'}
      </div>
      <div className="row end" style={{ marginTop: 14 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button className={result === '合格' ? '' : 'danger'} disabled={busy} onClick={submit}>上传复检结果</button>
      </div>
    </Modal>
  )
}
