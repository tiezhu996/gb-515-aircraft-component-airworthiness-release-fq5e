import type { DomainRecord } from '../../types/domain';
import { StatusBadge } from './StatusBadge';

const STATUS_LABEL: Record<string, string> = {
	passed: '已通过', valid: '有效', running: '进行中', planned: '已计划',
	failed: '未通过', draft: '草案', expired: '已过期', revoked: '已撤销',
};

function statusLabel(status?: string): string {
	if (!status) return '缺失';
	return STATUS_LABEL[status] || status;
}

function EvidenceRow({ label, frozenCode, frozenVersion, currentVersion, currentStatus, ok }: {
	label: string; frozenCode?: string; frozenVersion?: number;
	currentVersion?: number; currentStatus?: string; ok: boolean;
}) {
	const hasFrozen = Boolean(frozenCode);
	const drift = hasFrozen && (currentVersion !== frozenVersion || currentStatus !== (label === '检查任务' ? 'passed' : 'valid'));
	return <div className={hasFrozen ? (ok ? 'evidence-row evidence-row--ok' : 'evidence-row evidence-row--bad') : 'evidence-row evidence-row--pending'}>
		<span className="evidence-row__label">{label}</span>
		{hasFrozen
			? <><code>{frozenCode}</code><small>冻结 v{frozenVersion}（{label === '检查任务' ? '已通过' : '有效'}）</small>
				<small className={drift ? 'evidence-drift' : ''}>当前 {statusLabel(currentStatus)} {currentVersion ? `v${currentVersion}` : '缺失'}</small></>
			: <><code>未冻结</code><small>当前 {statusLabel(currentStatus)} {currentVersion ? `v${currentVersion}` : '—'}</small><small>提交复核时自动冻结</small></>}
	</div>;
}

export function EvidenceFreezePanel({ item }: { item: DomainRecord }) {
	const frozen = Boolean(item.frozenInspectionCode || item.frozenCertificateCode);
	const consistent = item.evidenceConsistent;
	const blockReason = item.evidenceBlockReason || item.evidenceBlockedReason || '';
	return <section className="evidence-freeze">
		<header>
			<div>
				<h2>证据冻结校验</h2>
				<span>关联编号 <code>{item.relatedCode || '未填写'}</code></span>
			</div>
			{consistent === true && <span className="evidence-chip evidence-chip--ok">证据一致</span>}
			{consistent === false && <span className="evidence-chip evidence-chip--bad">{frozen ? '证据已漂移' : '证据不完整'}</span>}
			{consistent == null && <span className="evidence-chip">待校验</span>}
		</header>
		<div className="evidence-grid">
			<EvidenceRow label="检查任务" frozenCode={item.frozenInspectionCode} frozenVersion={item.frozenInspectionVersion}
				currentVersion={item.currentInspectionVersion} currentStatus={item.currentInspectionStatus} ok={consistent === true} />
			<EvidenceRow label="证书" frozenCode={item.frozenCertificateCode} frozenVersion={item.frozenCertificateVersion}
				currentVersion={item.currentCertificateVersion} currentStatus={item.currentCertificateStatus} ok={consistent === true} />
		</div>
		{blockReason && <div className="evidence-block" role="alert">
			<strong>阻断原因{item.evidenceBlockCode ? `（${item.evidenceBlockCode}）` : ''}</strong>
			<span>{blockReason}</span>
		</div>}
		{!blockReason && item.status === 'approved' && <p className="evidence-note">已批准记录保留提交时冻结的证据，后续更新不得改写。</p>}
		{item.revisions && item.revisions.length > 0 && <div className="evidence-revisions">
			{item.revisions.slice(-4).map((revision) => <span key={revision.id} className="evidence-revision">
				<StatusBadge status={revision.status} /> v{revision.version}
				{revision.frozenInspectionCode && <small>冻结 {revision.frozenInspectionCode} v{revision.frozenInspectionVersion} / {revision.frozenCertificateCode} v{revision.frozenCertificateVersion}</small>}
				<small title={revision.requestId}>{revision.actor} · {revision.requestId}</small>
			</span>)}
		</div>}
	</section>;
}
