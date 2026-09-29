import React, { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, setSession } from '../api.js'
import { useToast } from '../components.jsx'

const ACCOUNTS = [
  { username: 'admin', name: '平台管理员（全权限）' },
  { username: 'brewery', name: '酒厂仓库·周厂长' },
  { username: 'qc', name: '品控·程品控' },
  { username: 'finance', name: '财务·钱会计' },
  { username: 'bar1', name: '泡沫实验室·阿May' },
  { username: 'bar2', name: '醉猫酒馆·老陈' },
  { username: 'bar3', name: '胡同里·祥子' },
  { username: 'driver1', name: '上海司机·王师傅' },
  { username: 'driver2', name: '北京司机·李师傅' },
  { username: 'wash1', name: '上海净桶中心' },
  { username: 'wash2', name: '北京洗桶站·马师傅' },
  { username: 'dealer1', name: '华东经销商·林经理' }
]

export default function Login() {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('demo123')
  const [loading, setLoading] = useState(false)
  const toast = useToast()
  const navigate = useNavigate()

  const submit = async (u, p) => {
    setLoading(true)
    try {
      const d = await api('/login', { method: 'POST', body: { username: u, password: p } })
      setSession(d.token, d.user)
      navigate('/')
    } catch (e) {
      toast(e.message, 'err')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-wrap">
      <form className="login-card" onSubmit={(e) => { e.preventDefault(); submit(username, password) }}>
        <div className="logo">🍺 精酿桶<span style={{ color: 'var(--brand)' }}>追溯</span>平台</div>
        <div className="sub">空桶回收 · 押金结算 · 批次追溯 · 召回通知</div>
        <label>账号</label>
        <input value={username} onChange={e => setUsername(e.target.value)} autoFocus />
        <label>密码</label>
        <input type="password" value={password} onChange={e => setPassword(e.target.value)} />
        <button style={{ width: '100%', marginTop: 18 }} disabled={loading}>
          {loading ? '登录中…' : '登 录'}
        </button>
        <div className="accounts">
          <div className="small muted" style={{ marginBottom: 6 }}>演示账号（点击填充，密码统一 demo123）：</div>
          {ACCOUNTS.map(a => (
            <span key={a.username} className="chip" onClick={() => setUsername(a.username)}>
              {a.name}
            </span>
          ))}
        </div>
      </form>
    </div>
  )
}
