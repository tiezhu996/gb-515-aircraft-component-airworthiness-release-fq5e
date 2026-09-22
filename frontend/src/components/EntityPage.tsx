
import { Fragment, useEffect, useMemo, useState } from 'react';
import type { EntityConfig, DomainRecord } from '../types/domain';
import type { EntityStore } from '../stores/factory';
import { nextStatus, formatDate } from '../utils/format';
import { StatusBadge } from './common/StatusBadge';
import { PartStatusBadge } from './common/PartStatusBadge';
import { MetricCard } from './common/MetricCard';
import { ConfirmDialog } from './common/ConfirmDialog';
import { UiButton } from './common/UiButton';
import { CertificatePanel } from './common/CertificatePanel';
import { EvidenceFreezePanel } from './common/EvidenceFreezePanel';
import { useAuth } from '../hooks/useAuth';

export function EntityPage({ config, useStore, certificateRecords = [], showEvidence = false }: { config: EntityConfig; useStore: EntityStore; certificateRecords?: DomainRecord[]; showEvidence?: boolean }) {
	const { items, meta, loading, error, load, createRecord, transition } = useStore();
	const { session, hasRole } = useAuth();
  const [search, setSearch] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [pending, setPending] = useState<{ item: DomainRecord; status: string } | null>(null);
  useEffect(() => { void load(config.path); }, [config.path, load]);
  const highRisk = useMemo(() => items.filter((item) => ['high', 'critical'].includes(item.riskLevel)).length, [items]);
  const createDemo = async () => {
    const now = Date.now();
    await createRecord(config.path, { code: `${config.key.toUpperCase()}-${now.toString().slice(-6)}`, name: `新增${config.label}`,
      description: '通过前端工作台创建的业务记录', facility: '默认作业区', owner: '现场操作员', category: '常规', riskLevel: 'medium',
      metricValue: 25, metricUnit: 'unit', effectiveAt: new Date().toISOString(), evidence: '已完成创建前检查',
      relatedCode: config.key === 'releaseAuthorization' ? 'REL-515-02' : '' });
    setShowCreate(false);
  };
	const role = session?.role || 'viewer';
	const canOperate = hasRole('operator');
	const requiresReviewer = (item: DomainRecord, target: string) =>
		(config.key === 'certificateRecord' && ['valid', 'revoked'].includes(target)) ||
		(config.key === 'releaseAuthorization' && item.status !== 'draft');
	const canAdvance = (item: DomainRecord, target: string) => canOperate && (!requiresReviewer(item, target) || hasRole('reviewer'));
	const usePartBadge = config.key === 'aircraftPart' || config.key === 'releaseAuthorization';
	const columnCount = showEvidence ? 9 : 8;
	return <main className="workspace">
		<header className="page-header"><div><p className="eyebrow">业务工作台</p><h1>{config.label}</h1><p>统一管理{config.label}的状态、风险、证据与责任人。</p></div>{canOperate ? <UiButton onClick={() => setShowCreate(true)}>新增{config.label}</UiButton> : <span className="access-note">只读权限</span>}</header>
		<section className="metrics"><MetricCard label="记录总数" value={meta.total} detail="当前筛选范围"/><MetricCard label="高风险" value={highRisk} detail="需要优先复核"/><MetricCard label="状态种类" value={new Set(items.map((item) => item.status)).size} detail="状态机覆盖"/></section>
		{certificateRecords.length > 0 && <section className="certificate-section"><header><h2>证书版本证据</h2><span>操作者与请求 ID 可追溯</span></header><CertificatePanel records={certificateRecords} /></section>}
		<section className="toolbar"><input aria-label="搜索" placeholder={`搜索${config.label}编码或名称`} value={search} onChange={(event) => setSearch(event.target.value)} /><UiButton onClick={() => void load(config.path, search)}>查询</UiButton><button className="link-button" onClick={() => { setSearch(''); void load(config.path); }}>重置</button></section>
    {error && <div className="alert" role="alert">{error}</div>}
    <section className="table-shell" aria-busy={loading}><table><thead><tr><th>编码</th><th>名称</th><th>状态</th><th>风险</th><th>责任人</th><th>指标</th>{showEvidence && <th>证据冻结</th>}<th>更新时间</th><th>操作</th></tr></thead><tbody>
			{items.map((item) => { const next = nextStatus(item.status, config.primaryTransitions); return <Fragment key={item.id}>
				<tr className={expanded === item.id ? 'row-expanded' : ''}>
					<td><strong>{item.code}</strong>{showEvidence && <button className="link-button" onClick={() => setExpanded(expanded === item.id ? null : item.id)}>{expanded === item.id ? '收起证据' : '查看证据'}</button>}</td>
					<td>{item.name}<small>{item.facility}</small></td><td>{usePartBadge ? <PartStatusBadge status={item.status}/> : <StatusBadge status={item.status}/>}</td><td>{item.riskLevel}</td><td>{item.owner}</td><td>{item.metricValue} {item.metricUnit}</td>
					{showEvidence && <td>{item.evidenceConsistent === true && <span className="evidence-chip evidence-chip--ok">一致</span>}{item.evidenceConsistent === false && <span className="evidence-chip evidence-chip--bad">{item.frozenInspectionCode ? '已漂移' : '不完整'}</span>}{item.evidenceConsistent == null && <span className="evidence-chip">待校验</span>}<small>检查 {(item.frozenInspectionCode ? `v${item.frozenInspectionVersion}` : '—')} / 当前 {item.currentInspectionVersion ? `v${item.currentInspectionVersion}` : '—'}</small><small>证书 {(item.frozenCertificateCode ? `v${item.frozenCertificateVersion}` : '—')} / 当前 {item.currentCertificateVersion ? `v${item.currentCertificateVersion}` : '—'}</small>{(item.evidenceBlockReason || item.evidenceBlockedReason) && <small className="evidence-drift" title={item.evidenceBlockReason || item.evidenceBlockedReason}>阻断：{item.evidenceBlockCode || '见详情'}</small>}</td>}
					<td>{formatDate(item.updatedAt)}</td><td>{next && canAdvance(item, next) ? <button className="table-action" onClick={() => setPending({ item, status: next })}>推进至 {next}</button> : next && requiresReviewer(item, next) && role === 'operator' ? <span className="muted">等待复核员</span> : next && !canOperate ? <span className="muted">只读</span> : <span className="muted">流程结束</span>}</td>
				</tr>
				{showEvidence && expanded === item.id && <tr className="evidence-detail-row"><td colSpan={columnCount}><EvidenceFreezePanel item={item} /></td></tr>}
			</Fragment>; })}
      {!items.length && !loading && <tr><td colSpan={columnCount} className="empty">暂无记录</td></tr>}
    </tbody></table>{loading && <div className="loading">正在同步业务数据…</div>}</section>
		<ConfirmDialog open={showCreate} title={`新增${config.label}`} onCancel={() => setShowCreate(false)} onConfirm={() => void createDemo().catch(() => undefined)}><p>将创建一条包含完整责任人、风险和证据信息的演示记录。</p>{showEvidence && <p>关联编号预填为 REL-515-02（已通过检查任务 + 有效证书），可直接提交复核验证冻结流程。</p>}</ConfirmDialog>
		<ConfirmDialog open={Boolean(pending)} title="确认状态迁移" onCancel={() => setPending(null)} onConfirm={() => { if (pending) { const target = pending.item.id; void transition(config.path, pending.item, pending.status).catch(() => setExpanded(target)).finally(() => setPending(null)); } }}><p>状态迁移会写入不可覆盖的版本与审计日志。</p><strong>{pending?.item.status} → {pending?.status}</strong>{showEvidence && pending?.status === 'review' && <p>提交时将按关联编号冻结已通过检查任务与有效证书的版本；证据缺失时保持草案并给出阻断编号。</p>}</ConfirmDialog>
	</main>;
}
