import React, { useEffect, useRef, useState } from 'react'
import { api, getToken } from '../api.js'
import { Badge, REWORK_STATUS, REWORK_DEFECT, BILL_STATUS, DEPOSIT, useToast, Empty, Modal, Money } from '../components.jsx'

const photoURL = (id) => `/api/rework/photo/${id}?token=${encodeURIComponent(getToken())}`

function readPhoto(file) {
  return new Promise((resolve, reject) => {
    const fr = new FileReader()
    fr.onload = () => resolve({
      file_name: file.name,
      content_type: file.type || 'image/png',
      data: fr.result.split(',')[1]
    })
    fr.onerror = reject
    fr.readAsDataURL(file)
  })
}

export default function ReworkPage() {
  const user = JSON.parse(localStorage.getItem('keg_user') || 'null')
  const isWash = user.role === 'washplant'
  const isBrewery = user.role === 'brewery'
  const isFinance = user.role === 'finance'
  const [tab, setTab] = useState(isWash ? 'pending' : 'orders')
  const [pending, setPending] = useState([])
  const [orders, setOrders] = useState([])
  const [counts, setCounts] = useState({})
  const [bills, setBills] = useState([])
  const [billSummary, setBillSummary] = useState(null)
  const [createFor, setCreateFor] = useState(null)
  const [reinspectFor, setReinspectFor] = useState(null)
  const [detailId, setDetailId] = useState(null)
  const toast = useToast()

  const loadOrders = () => api('/rework/orders').then(d => { setOrders(d.orders); setCounts(d.counts) })
  const loadBills = () => api('/finance/bills').then(d => { setBills(d.bills); setBillSummary(d.summary) })

  useEffect(() => {
    if (tab === 'pending') api('/rework/pending').then(d => setPending(d.pending))
    if (tab === 'orders') loadOrders()
    if (tab === 'bills') loadBills()
  }, [tab])

  const confirmRework = async (o, action) => {
    const note = prompt(action === 'restore'
      ? `确认 ${o.keg_code} 返修合格、恢复灌装？备注（可留空）：`
      : `确认 ${o.keg_code} 无法修复、予以报废？备注（可留空）：`) || ''
    try {
      const d = await api('/rework/confirm/' + o.id, { method: 'POST', body: { action, note } })
      toast(action === 'restore' ? '仓库已确认：桶回库，恢复再灌装资格' : '已报废处理')
      loadOrders()
    } catch (e) { toast(e.message, 'err') }
  }

  const confirmBill = async (b) => {
    if (!confirm(`确认账单 #${b.id}（${b.party_name}，¥${b.amount}）入账？`)) return
    try {
      await api('/finance/bills/confirm/' + b.id, { method: 'POST' })
      toast('账单已确认，财务账单同步更新')
      loadBills()
    } catch (e) { toast(e.message, 'err') }
  }

  return (
    <div>
      <h1>清洗不合格返修</h1>
      <div className="muted small" style={{ marginBottom: 12 }}>
        清洗厂发现桶内异味 / 阀门损坏等缺陷后发起返修，平台自动追溯<b>上一家酒吧、回收司机、清洗批次</b>，
        同步生成返修费账单、押金扣减并暂停再灌装；清洗厂复检上传后由<b>酒厂仓库确认</b>，桶才恢复可灌装，费用进入责任方账单。
      </div>

      <div className="toolbar">
        <TabBtn cur={tab} v="pending" show={isWash || user.role === 'admin'} onClick={() => setTab('pending')}>待发起返修</TabBtn>
        <TabBtn cur={tab} v="orders" onClick={() => setTab('orders')}>
          返修单
          {(counts.OPEN || counts.REWORKED) ?
            <span className="badge red" style={{ marginLeft: 6 }}>{(counts.OPEN || 0) + (counts.REWORKED || 0)}</span> : null}
        </TabBtn>
        <TabBtn cur={tab} v="bills" show={isFinance || isBrewery || isWash || user.role === 'admin' || user.role === 'bar_manager'} onClick={() => setTab('bills')}>
          返修账单{billSummary?.pending_count ? <span className="badge amber" style={{ marginLeft: 6 }}>{billSummary.pending_count}</span> : null}
        </TabBtn>
      </div>

      {tab === 'pending' && (
        <div className="tablewrap panel" style={{ padding: 0 }}>
          <table>
            <thead><tr>
              <th>桶编号</th><th>酒款/批次</th><th>追溯·上一家酒吧</th><th>追溯·回收司机</th>
              <th>清洗批次/检测</th><th>缺陷</th><th>押金</th><th>操作</th>
            </tr></thead>
            <tbody>
              {pending.map(c => (
                <tr key={c.cycle_id}>
                  <td><code className="kcode">{c.keg_code}</code><div className="small muted">{c.capacity_liters}L</div></td>
                  <td>{c.beer}（{c.style}）<div className="small muted">{c.batch_no}</div></td>
                  <td>{c.bar || '—'}<div className="small muted">ID {c.bar_id || '-'}</div></td>
                  <td>{c.driver || '—'}</td>
                  <td className="small">{c.wash_batch_no}<div className="muted">{c.washplant} · {c.checked_at}</div></td>
                  <td className="small" style={{ maxWidth: 200 }}>{c.defect_note || '清洗不合格'}</td>
                  <td className="small"><Money v={c.deposit_amount} /><div><Badge map={DEPOSIT} value={c.deposit_status} /></div></td>
                  <td><button className="small danger" onClick={() => setCreateFor(c)}>发起返修</button></td>
                </tr>
              ))}
            </tbody>
          </table>
          {pending.length === 0 && <Empty text="暂无清洗不合格、待发起返修的桶" />}
        </div>
      )}

      {tab === 'orders' && (
        <div className="tablewrap panel" style={{ padding: 0 }}>
          <table>
            <thead><tr>
              <th>返修单号</th><th>桶编号</th><th>缺陷</th><th>追溯链</th><th>返修费/承担方</th>
              <th>押金扣减</th><th>照片</th><th>状态</th><th>操作</th>
            </tr></thead>
            <tbody>
              {orders.map(o => (
                <tr key={o.id}>
                  <td><b>{o.rework_no}</b><div className="small muted">{o.created_at}</div></td>
                  <td><code className="kcode">{o.keg_code}</code><div className="small muted">{o.beer} {o.batch_no}</div></td>
                  <td><span className="badge red">{REWORK_DEFECT[o.defect_type] || o.defect_type}</span>
                    <div className="small muted" style={{ maxWidth: 160 }}>{o.defect_note}</div></td>
                  <td className="small">
                    酒吧：{o.bar || '—'}<br />司机：{o.driver || '—'}<br />
                    <span className="muted">清洗批次：{o.wash_batch_no || '—'}</span>
                  </td>
                  <td className="small"><Money v={o.rework_fee} />
                    <div>{o.bill_party === 'BAR' ? `酒吧：${o.bill_party_name || o.bar}` : `清洗厂：${o.bill_party_name}`}
                      {o.bill_status && <div><Badge map={BILL_STATUS} value={o.bill_status} /></div>}
                    </div>
                  </td>
                  <td>{o.deposit_deduct > 0 ? <b style={{ color: 'var(--danger)' }}><Money v={o.deposit_deduct} /></b> : <span className="muted">—</span>}</td>
                  <td className="small">发现照 {o.found_photos} 张<br />复检照 {o.reinspect_photos} 张</td>
                  <td><Badge map={REWORK_STATUS} value={o.status} />
                    {o.reinspect_result === 'STILL_FAIL' && o.status === 'OPEN' &&
                      <div className="badge amber" style={{ marginTop: 4 }}>复检仍不合格</div>}</td>
                  <td>
                    <button className="small ghost" onClick={() => setDetailId(o.id)}>详情/照片</button>
                    {isWash && o.status === 'OPEN' &&
                      <button className="small" style={{ marginTop: 4 }} onClick={() => setReinspectFor(o)}>上传复检结果</button>}
                    {(isBrewery || user.role === 'admin') && o.status === 'REWORKED' &&
                      <div style={{ marginTop: 4 }}>
                        <button className="small green" style={{ background: 'var(--ok)', borderColor: 'var(--ok)' }}
                          onClick={() => confirmRework(o, 'restore')}>确认恢复灌装</button>
                        <button className="small danger" style={{ marginTop: 4 }}
                          onClick={() => confirmRework(o, 'scrap')}>报废</button>
                      </div>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {orders.length === 0 && <Empty text="暂无返修单" />}
        </div>
      )}

      {tab === 'bills' && billSummary && (
        <>
          <div className="grid cols-3">
            <div className="panel stat warn"><span className="num"><Money v={billSummary.pending_amount} /></span><span className="lbl">待确认返修费（{billSummary.pending_count} 笔）</span></div>
            <div className="panel stat ok"><span className="num"><Money v={billSummary.confirmed_amount} /></span><span className="lbl">已入账返修费</span></div>
          </div>
          <div className="tablewrap panel" style={{ padding: 0 }}>
            <table>
              <thead><tr>
                <th>账单#</th><th>承担方</th><th>返修单</th><th>桶</th><th>费用项</th><th>金额</th><th>状态</th><th>操作</th>
              </tr></thead>
              <tbody>
                {bills.map(b => (
                  <tr key={b.id}>
                    <td>{b.id}<div className="small muted">{b.created_at}</div></td>
                    <td>{b.party_type === 'BAR' ? '🍺 ' : '🧽 '}{b.party_name}
                      <div className="small muted">{b.city || '清洗厂'}</div></td>
                    <td>{b.rework_no || '—'}{b.rework_status && <div className="small muted">返修单：{b.rework_status}</div>}</td>
                    <td><code className="kcode">{b.keg_code}</code></td>
                    <td className="small" style={{ maxWidth: 260 }}>{b.item}</td>
                    <td><b><Money v={b.amount} /></b></td>
                    <td><Badge map={BILL_STATUS} value={b.status} />
                      {b.confirmed_at && <div className="small muted">{b.confirmed_at}</div>}</td>
                    <td>
                      {(isFinance || user.role === 'admin') && b.status === 'PENDING'
                        ? <button className="small" onClick={() => confirmBill(b)}>确认入账</button>
                        : <span className="muted small">—</span>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {bills.length === 0 && <Empty text="暂无返修账单" />}
          </div>
        </>
      )}

      {createFor && <CreateReworkModal c={createFor} onClose={() => setCreateFor(null)}
        onDone={() => { setCreateFor(null); setTab('orders'); loadOrders() }} />}
      {reinspectFor && <ReinspectModal o={reinspectFor} onClose={() => setReinspectFor(null)}
        onDone={() => { setReinspectFor(null); loadOrders() }} />}
      {detailId && <ReworkDetailModal id={detailId} onClose={() => setDetailId(null)} />}
    </div>
  )
}

function TabBtn({ cur, v, show = true, children, onClick }) {
  if (!show) return null
  return <button className={cur === v ? '' : 'ghost'} onClick={onClick} style={{ marginRight: 8 }}>{children}</button>
}

// ---------- 发起返修 ----------
function CreateReworkModal({ c, onClose, onDone }) {
  const [defectType, setDefectType] = useState('VALVE_DAMAGED')
  const [note, setNote] = useState(c.defect_note || '')
  const [fee, setFee] = useState(200)
  const [billParty, setBillParty] = useState('BAR')
  const [deduct, setDeduct] = useState(0)
  const [files, setFiles] = useState([]) // {file, preview, payload}
  const [busy, setBusy] = useState(false)
  const fileRef = useRef()
  const toast = useToast()
  const depositHeld = c.deposit_status === 'HELD'

  const pick = async (list) => {
    const arr = Array.from(list).slice(0, 6)
    const next = []
    for (const file of arr) {
      const payload = await readPhoto(file)
      next.push({ file, preview: URL.createObjectURL(file), payload })
    }
    setFiles(f => [...f, ...next])
  }

  const submit = async () => {
    if (!note.trim()) { toast('请填写缺陷描述', 'err'); return }
    if (files.length === 0) { toast('请至少上传一张桶体照片', 'err'); return }
    if (deduct > c.deposit_amount) { toast('押金扣减不能超过押金总额', 'err'); return }
    setBusy(true)
    try {
      const d = await api('/rework/orders', {
        method: 'POST',
        body: {
          cycle_id: c.cycle_id, defect_type: defectType, defect_note: note,
          rework_fee: Number(fee) || 0, bill_party: billParty,
          deposit_deduct: depositHeld ? Number(deduct) || 0 : 0,
          photos: files.map(f => f.payload)
        }
      })
      toast(`返修单 ${d.rework_no} 已生成：费用账单与押金扣减同步完成，桶暂停再灌装`)
      onDone()
    } catch (e) { toast(e.message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal wide title={`发起清洗不合格返修 · ${c.keg_code}`} onClose={onClose}>
      <div className="grid cols-2">
        <div className="panel" style={{ background: '#faf6ec' }}>
          <h3>平台追溯信息</h3>
          <dl className="kv">
            <dt>酒款 / 批次</dt><dd>{c.beer}（{c.style}）{c.batch_no}</dd>
            <dt>上一家酒吧</dt><dd>{c.bar || '—'}</dd>
            <dt>回收司机</dt><dd>{c.driver || '—'}</dd>
            <dt>清洗批次</dt><dd>{c.wash_batch_no}</dd>
            <dt>检测清洗厂</dt><dd>{c.washplant} · {c.checked_at}</dd>
            <dt>原检测缺陷</dt><dd>{c.defect_note || '清洗不合格'}</dd>
          </dl>
        </div>
        <div>
          <label>缺陷类型</label>
          <select value={defectType} onChange={e => setDefectType(e.target.value)}>
            <option value="ODOR">桶内异味</option>
            <option value="VALVE_DAMAGED">阀门损坏</option>
            <option value="DEFORMED">桶体变形</option>
            <option value="RESIDUE">内壁残液</option>
            <option value="OTHER">其他</option>
          </select>
          <label>缺陷描述与返修方案</label>
          <textarea rows={3} value={note} onChange={e => setNote(e.target.value)}
            placeholder="如：阀门损坏无法保压，桶内有明显异味，更换阀门并做除味处理" />
          <div className="grid cols-2">
            <div>
              <label>返修费用（元）</label>
              <input type="number" min="0" value={fee} onChange={e => setFee(e.target.value)} />
            </div>
            <div>
              <label>费用承担方</label>
              <select value={billParty} onChange={e => setBillParty(e.target.value)}>
                <option value="BAR">上一家酒吧</option>
                <option value="WASHPLANT">清洗厂（工艺责任）</option>
              </select>
            </div>
            <div>
              <label>押金扣减（元）{!depositHeld && <span className="badge gray" style={{ marginLeft: 6 }}>押金已结算，不可再扣</span>}</label>
              <input type="number" min="0" max={c.deposit_amount} disabled={!depositHeld}
                value={depositHeld ? deduct : 0} onChange={e => setDeduct(e.target.value)} />
              <div className="small muted">押金总额 <Money v={c.deposit_amount} /></div>
            </div>
          </div>
        </div>
      </div>

      <label style={{ marginTop: 10 }}>桶体照片（仓库留存，至少 1 张，PNG/JPEG/WebP，单张 ≤5MB）</label>
      <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" multiple
        onChange={e => pick(e.target.files)} />
      <div className="row" style={{ flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
        {files.map((f, i) => (
          <div key={i} style={{ position: 'relative' }}>
            <img src={f.preview} alt={f.file.name}
              style={{ width: 110, height: 110, objectFit: 'cover', borderRadius: 6, border: '1px solid #ddd' }} />
            <button className="subtle small" style={{ position: 'absolute', top: 2, right: 2 }}
              onClick={() => setFiles(fs => fs.filter((_, j) => j !== i))}>✕</button>
            <div className="small muted" style={{ width: 110, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{f.file.name}</div>
          </div>
        ))}
      </div>

      <div className="suggest danger" style={{ marginTop: 12 }}>
        提交后：桶保持「清洗不合格·暂停再灌装」，返修费进入{billParty === 'BAR' ? '酒吧' : '清洗厂'}账单（待财务确认）
        {deduct > 0 && depositHeld ? `，并从押金扣减 ¥${deduct}` : ''}；返修完成需复检上传、酒厂仓库确认后才恢复可灌装。
      </div>
      <div className="row end" style={{ marginTop: 14 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button className="danger" disabled={busy} onClick={submit}>确认发起返修</button>
      </div>
    </Modal>
  )
}

// ---------- 复检上传 ----------
function ReinspectModal({ o, onClose, onDone }) {
  const [pressure, setPressure] = useState(true)
  const [seal, setSeal] = useState(true)
  const [clean, setClean] = useState(true)
  const [note, setNote] = useState('阀门已更换，除味处理完成，复检无异味')
  const [inspector, setInspector] = useState('马师傅')
  const [files, setFiles] = useState([])
  const [busy, setBusy] = useState(false)
  const toast = useToast()
  const passed = pressure && seal && clean

  const pick = async (list) => {
    const arr = Array.from(list).slice(0, 6)
    const next = []
    for (const file of arr) {
      const payload = await readPhoto(file)
      next.push({ file, preview: URL.createObjectURL(file), payload })
    }
    setFiles(f => [...f, ...next])
  }

  const submit = async () => {
    setBusy(true)
    try {
      const d = await api('/rework/reinspect/' + o.id, {
        method: 'POST',
        body: {
          passed, pressure_ok: pressure, seal_ok: seal, cleanliness_ok: clean,
          note, inspector, photos: files.map(f => f.payload)
        }
      })
      toast(d.passed ? '复检合格，已提交酒厂仓库确认，确认后恢复灌装' : '复检仍不合格，单据保持返修中')
      onDone()
    } catch (e) { toast(e.message, 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={`返修复检上传 · ${o.keg_code}（${o.rework_no}）`} onClose={onClose}>
      <div className="muted small" style={{ marginBottom: 8 }}>
        缺陷：{REWORK_DEFECT[o.defect_type]} · {o.defect_note}
      </div>
      <div className="checks">
        <label><input type="checkbox" checked={pressure} onChange={e => setPressure(e.target.checked)} /> 耐压复检合格</label>
        <label><input type="checkbox" checked={seal} onChange={e => setSeal(e.target.checked)} /> 阀门/密封复检合格</label>
        <label><input type="checkbox" checked={clean} onChange={e => setClean(e.target.checked)} /> 内壁无异味、清洁合格</label>
      </div>
      <label>复检员</label>
      <input value={inspector} onChange={e => setInspector(e.target.value)} />
      <label>复检说明</label>
      <textarea rows={3} value={note} onChange={e => setNote(e.target.value)} />
      <label>复检桶体照片（可选）</label>
      <input type="file" accept="image/png,image/jpeg,image/webp" multiple onChange={e => pick(e.target.files)} />
      <div className="row" style={{ flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
        {files.map((f, i) => (
          <div key={i} style={{ position: 'relative' }}>
            <img src={f.preview} alt={f.file.name}
              style={{ width: 100, height: 100, objectFit: 'cover', borderRadius: 6, border: '1px solid #ddd' }} />
            <button className="subtle small" style={{ position: 'absolute', top: 2, right: 2 }}
              onClick={() => setFiles(fs => fs.filter((_, j) => j !== i))}>✕</button>
          </div>
        ))}
      </div>
      <div className={'suggest ' + (passed ? '' : 'danger')} style={{ marginTop: 12 }}>
        {passed ? '判定：复检合格 → 提交酒厂仓库确认，确认后桶回库恢复可灌装'
                : '判定：复检仍不合格 → 单据保持返修中，继续处理后重新上传'}
      </div>
      <div className="row end" style={{ marginTop: 14 }}>
        <button className="subtle" onClick={onClose}>取消</button>
        <button className={passed ? '' : 'danger'} disabled={busy} onClick={submit}>提交复检结果</button>
      </div>
    </Modal>
  )
}

// ---------- 返修单详情/照片 ----------
function ReworkDetailModal({ id, onClose }) {
  const [d, setD] = useState(null)
  useEffect(() => { api('/rework/orders/' + id).then(x => setD(x.order)) }, [id])
  if (!d) return <Modal title="返修单详情" onClose={onClose}><div className="muted">加载中…</div></Modal>
  const found = d.photos.filter(p => p.phase === 'FOUND')
  const recheck = d.photos.filter(p => p.phase === 'REINSPECT')

  const PhotoBox = ({ p }) => (
    <a href={photoURL(p.id)} target="_blank" rel="noreferrer">
      <img src={photoURL(p.id)} title={p.file_name}
        style={{ width: 130, height: 130, objectFit: 'cover', borderRadius: 6, border: '1px solid #ccc' }} />
    </a>
  )

  return (
    <Modal wide title={`返修单 ${d.rework_no} · ${d.keg_code}`} onClose={onClose}>
      <div className="grid cols-2">
        <div>
          <h3>追溯与费用</h3>
          <dl className="kv">
            <dt>状态</dt><dd><Badge map={REWORK_STATUS} value={d.status} /></dd>
            <dt>酒款/批次</dt><dd>{d.beer}（{d.style}）{d.batch_no}</dd>
            <dt>上一家酒吧</dt><dd>{d.bar || '—'}</dd>
            <dt>回收司机</dt><dd>{d.driver || '—'}</dd>
            <dt>清洗批次</dt><dd>{d.wash_batch_no || '—'}</dd>
            <dt>清洗厂</dt><dd>{d.washplant}</dd>
            <dt>缺陷</dt><dd>{REWORK_DEFECT[d.defect_type]} · {d.defect_note}</dd>
            <dt>首次检测</dt><dd>
              耐压{d.first_check.pressure_ok ? '✓' : '✗'} · 密封{d.first_check.seal_ok ? '✓' : '✗'}
              · 清洁{d.first_check.cleanliness_ok ? '✓' : '✗'}（{d.first_check.inspector || '—'}）
            </dd>
            <dt>返修费</dt><dd><Money v={d.rework_fee} /> · {d.bill_party === 'BAR' ? `酒吧承担：${d.bar}` : '清洗厂承担'}
              {d.bill_status && <span style={{ marginLeft: 8 }}><Badge map={BILL_STATUS} value={d.bill_status} /></span>}</dd>
            <dt>押金</dt><dd>总额 <Money v={d.deposit_amount} /> · 扣减 <b style={{ color: 'var(--danger)' }}><Money v={d.deposit_deduct} /></b>
              <span style={{ marginLeft: 8 }}><Badge map={DEPOSIT} value={d.deposit_status} /></span></dd>
            {d.reinspect_result && <>
              <dt>复检结果</dt><dd>{d.reinspect_result === 'PASSED' ? '✅ 合格' : '❌ 仍不合格'}（{d.reinspector}，{d.reinspected_at}）
                {d.reinspect_note && <div className="small muted">{d.reinspect_note}</div>}</dd>
              <dt>仓库确认</dt><dd>{d.confirmed_at || '待确认'}</dd>
            </>}
          </dl>
        </div>
        <div>
          <h3>桶体照片（仓库留存）</h3>
          <div className="small muted" style={{ marginBottom: 6 }}>发现缺陷照片（{found.length}）</div>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
            {found.map(p => <PhotoBox key={p.id} p={p} />)}
            {found.length === 0 && <span className="muted small">无</span>}
          </div>
          <div className="small muted" style={{ margin: '12px 0 6px' }}>复检照片（{recheck.length}）</div>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
            {recheck.map(p => <PhotoBox key={p.id} p={p} />)}
            {recheck.length === 0 && <span className="muted small">清洗厂尚未上传复检照片</span>}
          </div>
        </div>
      </div>
    </Modal>
  )
}
