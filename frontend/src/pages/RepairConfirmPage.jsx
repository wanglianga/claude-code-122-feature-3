import React, { useEffect, useState } from 'react'
import { api } from '../api.js'
import { Badge, REPAIR_STATUS, Money, Empty, useToast } from '../components.jsx'
import { RepairDetailModal } from './repair-ui.jsx'

// 酒厂仓库：复检确认清洗厂返修结果，合格后桶才恢复可灌装
export default function RepairConfirmPage() {
  const [data, setData] = useState(null)
  const [filter, setFilter] = useState('SUBMITTED')
  const [detail, setDetail] = useState(null)
  const toast = useToast()
  const load = () => api('/repairs?status=' + filter).then(setData)
  useEffect(load, [filter])

  const decide = async (r, pass) => {
    let note = ''
    if (pass) {
      note = prompt(`确认 ${r.keg_code}（${r.repair_no}）返修复检合格？确认后桶回库并恢复再灌装资格。确认意见（可留空）：`, '复检照片齐全，桶况达标，同意恢复灌装') || ''
      if (note === null) return
    } else {
      note = prompt('退回原因（要求清洗厂重新返修）：', '复检不达标，请重新深度处理后再次提交') || ''
      if (!note) return
    }
    try {
      const d = await api('/repairs/confirm/' + r.id, { method: 'POST', body: { pass, note } })
      toast(pass ? `已确认：${d.keg_code} 回库恢复再灌装` : '已退回清洗厂继续返修', pass ? 'ok' : 'err')
      load()
    } catch (e) { toast(e.message, 'err') }
  }

  return (
    <div>
      <h1>返修复检确认（酒厂仓库）</h1>
      <div className="muted small" style={{ marginBottom: 12 }}>
        清洗厂返修完成并上传检测结果与桶体照片后在此确认：<b>确认合格该桶才恢复可灌装并回库</b>；不合格可退回清洗厂继续返修。桶体照片由仓库长期保留。
      </div>
      <div className="toolbar">
        <div className="f"><label>返修单视图</label>
          <select value={filter} onChange={e => setFilter(e.target.value)}>
            <option value="SUBMITTED">待我确认</option>
            <option value="OPEN">清洗厂返修中</option>
            <option value="CONFIRMED">已确认回库</option>
          </select></div>
        <button className="subtle" onClick={load}>刷新</button>
      </div>

      <div className="tablewrap panel" style={{ padding: 0 }}>
        <table>
          <thead><tr>
            <th>返修单/桶</th><th>缺陷</th><th>追溯（酒吧/司机/清洗批次）</th>
            <th>费用</th><th>清洗厂复检</th><th>照片</th><th>状态</th><th>操作</th>
          </tr></thead>
          <tbody>
            {(data?.repairs || []).map(r => (
              <tr key={r.id}>
                <td><b>{r.repair_no}</b><br /><code className="kcode">{r.keg_code}</code></td>
                <td className="small">{r.defect_type}<div className="muted">{r.defect_note}</div></td>
                <td className="small">{r.bar}<div className="muted">{r.driver}</div><div className="muted">{r.wash_batch_no}</div></td>
                <td className="small">返修费 <Money v={r.repair_fee} /><div className="muted">押金扣减 ¥{r.deposit_deduct}</div></td>
                <td className="small">{r.recheck_result ? <b style={{ color: r.recheck_result === '合格' ? 'var(--ok)' : 'var(--danger)' }}>{r.recheck_result}</b> : '—'}
                  {r.submitted_at && <div className="muted">{r.submitted_at}</div>}</td>
                <td>{r.photo_count} 张<br /><button className="small ghost" onClick={() => setDetail(r.id)}>查看</button></td>
                <td><Badge map={REPAIR_STATUS} value={r.status} /></td>
                <td>
                  {r.status === 'SUBMITTED'
                    ? <div className="flags" style={{ flexDirection: 'column', alignItems: 'flex-start' }}>
                        <button className="small green" style={{ background: 'var(--ok)', borderColor: 'var(--ok)' }}
                          onClick={() => decide(r, true)}>确认合格·恢复灌装</button>
                        <button className="small danger" onClick={() => decide(r, false)}>不合格退回</button>
                      </div>
                    : <button className="small ghost" onClick={() => setDetail(r.id)}>详情</button>}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {data && data.repairs.length === 0 && <Empty text={filter === 'SUBMITTED' ? '暂无待确认的返修复检单' : '暂无返修单'} />}
      </div>

      {detail && <RepairDetailModal id={detail} onClose={() => { setDetail(null); load() }} />}
    </div>
  )
}
