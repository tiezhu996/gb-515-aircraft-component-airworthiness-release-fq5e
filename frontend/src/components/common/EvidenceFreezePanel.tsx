
import type { DomainRecord } from '../../types/domain';
import { StatusBadge } from './StatusBadge';

function versionPair(frozen: number | undefined, current: number | undefined, hasFrozen: boolean): string {
	if (hasFrozen) {
		return `冻结 v${frozen ?? 0} / 当前 v${current ?? 0}`;
	}
	return `当前 v${current ?? 0}`;
}

// EvidenceFreezePanel renders the evidence freeze read model for 放行授权:
// related code, frozen/current inspection and certificate versions, live
// consistency and the blocking code/reason. All values come from the API so
// they survive a page refresh.
export function EvidenceFreezePanel({ records }: { records: DomainRecord[] }) {
	if (!records.length) return <div className="empty">暂无放行记录</div>;
	return <div className="freeze-list">{records.map((item) => {
		const freeze = item.evidenceStatus;
		const related = freeze?.relatedCode || item.relatedCode || '-';
		const hasFrozen = item.status !== 'draft';
		const consistent = freeze?.consistent ?? true;
		const inspectionCode = freeze?.inspectionCode || item.frozenInspectionCode || '未找到';
		const certificateCode = freeze?.certificateCode || item.frozenCertificateCode || '未找到';
		return <article key={item.id} className={consistent ? 'freeze-card freeze-card--ok' : 'freeze-card freeze-card--blocked'}>
			<div className="freeze-head">
				<strong>{item.code}</strong>
				<StatusBadge status={item.status} />
				<span className={`freeze-signal ${consistent ? 'freeze-signal--ok' : 'freeze-signal--blocked'}`}>
					{consistent ? '证据一致' : '证据阻断'}
				</span>
			</div>
			<small>关联编号：<code>{related}</code></small>
			<div className="freeze-row">
				<span>检查任务 {inspectionCode}{freeze?.inspectionStatus ? `（${freeze.inspectionStatus}）` : ''}</span>
				<span>{versionPair(item.frozenInspectionVersion, freeze?.currentInspectionVersion, hasFrozen)}</span>
			</div>
			<div className="freeze-row">
				<span>证书 {certificateCode}{freeze?.certificateStatus ? `（${freeze.certificateStatus}）` : ''}</span>
				<span>{versionPair(item.frozenCertificateVersion, freeze?.currentCertificateVersion, hasFrozen)}</span>
			</div>
			{!consistent && freeze?.blockCode && <div className="freeze-reason" role="alert">
				<code>{freeze.blockCode}</code>
				<span>{freeze.blockReason}</span>
			</div>}
			{consistent && hasFrozen && item.reviewedBy && <small>已由 {item.reviewedBy} 批准，冻结证据保留不可改写</small>}
		</article>;
	})}</div>;
}
