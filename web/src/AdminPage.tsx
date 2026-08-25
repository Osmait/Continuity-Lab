import {
	useCallback,
	useEffect,
	useMemo,
	useState,
	type ReactNode,
} from "react";
import { Link, useLocation } from "react-router-dom";
import {
	api,
	repositoryPath,
	type ClusterResponse,
	type RepositorySummary,
	type WALEntry,
	type WALResponse,
} from "./api";
import { ErrorBanner, Loading } from "./components";
import { BranchIcon, HistoryIcon, RepoIcon, ServerIcon } from "./icons";
import { relativeTime, shortOID } from "./utils";

type AdminSection = "overview" | "nodes" | "wal" | "storage";
type RepositoryWAL = WALEntry & { repository: string };

const sections: Array<{ id: AdminSection; label: string; icon: ReactNode }> = [
	{ id: "overview", label: "Overview", icon: <span>◱</span> },
	{ id: "nodes", label: "Nodes", icon: <ServerIcon /> },
	{ id: "wal", label: "WAL log", icon: <HistoryIcon /> },
	{ id: "storage", label: "Storage", icon: <RepoIcon /> },
];

export function AdminPage() {
	const location = useLocation();
	const requested = location.pathname.split("/")[2] as AdminSection | undefined;
	const section = sections.some((item) => item.id === requested)
		? (requested as AdminSection)
		: "overview";
	const [repositories, setRepositories] = useState<RepositorySummary[]>([]);
	const [cluster, setCluster] = useState<ClusterResponse | null>(null);
	const [walEntries, setWALEntries] = useState<RepositoryWAL[]>([]);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<unknown>(null);

	const load = useCallback(async () => {
		setLoading(true);
		setError(null);
		try {
			const [repositoryResult, clusterResult] = await Promise.all([
				api<{ repositories: RepositorySummary[] }>("/api/v1/repos"),
				api<ClusterResponse>("/api/v1/cluster"),
			]);
			setRepositories(repositoryResult.repositories);
			setCluster(clusterResult);

			const histories = await Promise.all(
				repositoryResult.repositories
					.filter((repository) => repository.sequence > 0)
					.map(async (repository) => {
						const result = await api<WALResponse>(
							`/api/v1/repos/${repositoryPath(repository.name)}/wal?limit=15`,
						);
						return result.entries_newest_first.map((entry) => ({
							...entry,
							repository: repository.name,
						}));
					}),
			);
			setWALEntries(
				histories
					.flat()
					.sort(
						(left, right) =>
							Date.parse(right.created_at) - Date.parse(left.created_at),
					)
					.slice(0, 100),
			);
		} catch (caught) {
			setError(caught);
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		void load();
	}, [load]);

	const healthyNodes =
		cluster?.nodes.filter((node) => node.healthy).length ?? 0;
	const totalSequences = repositories.reduce(
		(sum, repo) => sum + repo.sequence,
		0,
	);
	const totalRefs = repositories.reduce((sum, repo) => sum + repo.ref_count, 0);
	const currentSection = sections.find((item) => item.id === section)!;

	return (
		<div className="admin-shell">
			<aside className="admin-sidebar">
				<div className="admin-brand">
					<span className="admin-brand-mark">▧</span>
					<div>
						<strong>Ops Console</strong>
						<small>INTERNAL · LAB</small>
					</div>
				</div>
				<nav className="admin-nav" aria-label="Administration">
					<span className="admin-nav-label">Infrastructure</span>
					{sections.map((item) => (
						<Link
							key={item.id}
							className={section === item.id ? "active" : ""}
							to={item.id === "overview" ? "/admin" : `/admin/${item.id}`}
						>
							{item.icon}
							<span>{item.label}</span>
							{item.id === "nodes" && (
								<i className={healthyNodes === 3 ? "ready" : ""} />
							)}
							{item.id === "wal" && <code>{totalSequences}</code>}
						</Link>
					))}
				</nav>
				<div className="admin-sidebar-foot">
					<div>
						<strong>Read-only console</strong>
						<span>
							Operational data comes directly from the current gateway APIs.
						</span>
					</div>
					<Link to="/">↩ Return to Continuity Git</Link>
				</div>
			</aside>

			<div className="admin-main">
				<header className="admin-topbar">
					<div>
						<span>ops</span>
						<b>/</b>
						<strong>
							{currentSection.label.toLowerCase().replace(" ", "-")}
						</strong>
					</div>
					<div className="admin-top-actions">
						<span
							className={`admin-health ${healthyNodes === 3 ? "ready" : ""}`}
						>
							<i />
							{healthyNodes}/3 {healthyNodes === 3 ? "READY" : "DEGRADED"}
						</span>
						<button
							className="admin-action"
							onClick={() => window.location.assign("/metrics")}
						>
							Prometheus
						</button>
						<button className="admin-action" onClick={() => void load()}>
							Re-check now
						</button>
					</div>
				</header>

				{loading ? (
					<Loading label="Reading operational state…" />
				) : error ? (
					<div className="admin-error">
						<ErrorBanner error={error} retry={() => void load()} />
					</div>
				) : (
					<AdminContent
						section={section}
						repositories={repositories}
						cluster={cluster}
						walEntries={walEntries}
						totalSequences={totalSequences}
						totalRefs={totalRefs}
					/>
				)}
			</div>
		</div>
	);
}

function AdminContent({
	section,
	repositories,
	cluster,
	walEntries,
	totalSequences,
	totalRefs,
}: {
	section: AdminSection;
	repositories: RepositorySummary[];
	cluster: ClusterResponse | null;
	walEntries: RepositoryWAL[];
	totalSequences: number;
	totalRefs: number;
}) {
	if (section === "nodes") return <NodesSection cluster={cluster} />;
	if (section === "wal") return <WALSection entries={walEntries} />;
	if (section === "storage")
		return (
			<StorageSection
				repositories={repositories}
				totalSequences={totalSequences}
			/>
		);
	return (
		<OverviewSection
			repositories={repositories}
			cluster={cluster}
			totalSequences={totalSequences}
			totalRefs={totalRefs}
		/>
	);
}

function OverviewSection({
	repositories,
	cluster,
	totalSequences,
	totalRefs,
}: {
	repositories: RepositorySummary[];
	cluster: ClusterResponse | null;
	totalSequences: number;
	totalRefs: number;
}) {
	const healthy = cluster?.nodes.filter((node) => node.healthy).length ?? 0;
	const latest = useMemo(
		() =>
			[...repositories]
				.sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at))
				.slice(0, 8),
		[repositories],
	);
	return (
		<div className="admin-overview">
			<section className="admin-metrics">
				<AdminMetric
					label="Repositories"
					value={repositories.length}
					detail="authoritative manifests"
				/>
				<AdminMetric
					label="Committed sequences"
					value={totalSequences}
					detail="across all repositories"
				/>
				<AdminMetric
					label="Published refs"
					value={totalRefs}
					detail="visible in head.json"
				/>
				<AdminMetric
					label="Healthy nodes"
					value={`${healthy}/3`}
					detail="gateway readiness checks"
				/>
			</section>
			<div className="admin-overview-grid">
				<section className="admin-panel">
					<PanelHeader
						title="Nodes"
						meta={`checked by gateway · ${healthy}/3 healthy`}
					/>
					<NodeRows cluster={cluster} />
				</section>
				<section className="admin-panel">
					<PanelHeader title="Recent repository heads" meta="MinIO state" />
					{latest.map((repo) => (
						<RepositoryOperationalRow key={repo.repo_id} repository={repo} />
					))}
				</section>
			</div>
		</div>
	);
}

function NodesSection({ cluster }: { cluster: ClusterResponse | null }) {
	return (
		<div className="admin-section-pad">
			<div className="admin-section-heading">
				<div>
					<span>NODES</span>
					<h1>Disposable Git nodes</h1>
					<p>
						Gateway readiness observations for each independent repository
						cache.
					</p>
				</div>
			</div>
			<div className="node-card-grid">
				{cluster?.nodes.map((status) => (
					<article className="node-card" key={status.node.id}>
						<div className="node-card-head">
							<strong>
								<i className={status.healthy ? "ready" : ""} />
								{status.node.id}
							</strong>
							<span>{status.healthy ? "READY" : "UNAVAILABLE"}</span>
						</div>
						<AdminDetail label="Internal URL" value={status.node.url} />
						<AdminDetail
							label="Checked"
							value={relativeTime(status.checked_at)}
						/>
						<AdminDetail label="Role" value="Disposable cache" />
					</article>
				))}
			</div>
			<p className="admin-disclosure">
				Sequence lag, uptime, and filesystem usage are not displayed because the
				current API does not expose them.
			</p>
		</div>
	);
}

function WALSection({ entries }: { entries: RepositoryWAL[] }) {
	return (
		<div className="admin-section">
			<div className="admin-section-heading padded">
				<div>
					<span>WAL LOG</span>
					<h1>Authoritative commit log</h1>
					<p>
						Newest committed entries across repositories, read through each
						head.json chain.
					</p>
				</div>
			</div>
			<div className="admin-table-head wal-columns">
				<span>Seq</span>
				<span>Repository</span>
				<span>Ref updates</span>
				<span>Node</span>
				<span>Committed</span>
			</div>
			{entries.length === 0 ? (
				<div className="admin-empty">No committed WAL entries.</div>
			) : (
				entries.map((entry) => (
					<div
						className="admin-table-row wal-columns"
						key={`${entry.repository}-${entry.entry_id}`}
					>
						<code>{entry.sequence}</code>
						<Link to={`/repos/${entry.repository}`}>{entry.repository}</Link>
						<span className="wal-summary">
							{entry.updates
								.map(
									(update) =>
										`${update.ref.replace("refs/heads/", "")} → ${shortOID(update.new_oid)}`,
								)
								.join(", ")}
						</span>
						<code>{entry.node_id}</code>
						<span>{relativeTime(entry.created_at)}</span>
					</div>
				))
			)}
			<div className="admin-table-note">
				Showing at most 15 entries per repository and 100 entries overall.
			</div>
		</div>
	);
}

function StorageSection({
	repositories,
	totalSequences,
}: {
	repositories: RepositorySummary[];
	totalSequences: number;
}) {
	return (
		<div className="admin-section-pad">
			<div className="admin-section-heading">
				<div>
					<span>STORAGE</span>
					<h1>Authoritative MinIO records</h1>
					<p>
						Logical durable records visible through the current repository API.
					</p>
				</div>
			</div>
			<div className="storage-card-grid">
				<StorageCard
					title="Repository manifests"
					value={repositories.length}
					detail="immutable manifest.json records"
				/>
				<StorageCard
					title="CAS heads"
					value={repositories.length}
					detail="one mutable head.json per repository"
				/>
				<StorageCard
					title="Committed sequences"
					value={totalSequences}
					detail="durable WAL history published by CAS"
				/>
			</div>
			<section className="admin-panel storage-records">
				<PanelHeader
					title="Repository storage index"
					meta="authoritative metadata"
				/>
				{repositories.map((repo) => (
					<RepositoryOperationalRow key={repo.repo_id} repository={repo} />
				))}
			</section>
			<p className="admin-disclosure">
				Physical bucket size and object counts are omitted because MinIO usage
				statistics are not exposed by the gateway API.
			</p>
		</div>
	);
}

function NodeRows({ cluster }: { cluster: ClusterResponse | null }) {
	return (
		<>
			{cluster?.nodes.map((status) => (
				<div className="admin-node-row" key={status.node.id}>
					<strong>
						<i className={status.healthy ? "ready" : ""} />
						{status.node.id}
					</strong>
					<code>{status.node.url}</code>
					<span>{relativeTime(status.checked_at)}</span>
					<b>{status.healthy ? "READY" : "DOWN"}</b>
				</div>
			))}
		</>
	);
}

function RepositoryOperationalRow({
	repository,
}: {
	repository: RepositorySummary;
}) {
	return (
		<Link className="admin-repo-row" to={`/repos/${repository.name}`}>
			<div>
				<strong>{repository.name}</strong>
				<code>{repository.repo_id.slice(0, 12)}</code>
			</div>
			<span>
				<BranchIcon />
				{repository.ref_count} refs
			</span>
			<code>seq {repository.sequence}</code>
			<span>{relativeTime(repository.updated_at)}</span>
		</Link>
	);
}

function AdminMetric({
	label,
	value,
	detail,
}: {
	label: string;
	value: string | number;
	detail: string;
}) {
	return (
		<div>
			<span>{label}</span>
			<strong>{value}</strong>
			<small>{detail}</small>
		</div>
	);
}

function PanelHeader({ title, meta }: { title: string; meta: string }) {
	return (
		<header className="admin-panel-head">
			<strong>{title}</strong>
			<span>{meta}</span>
		</header>
	);
}

function AdminDetail({ label, value }: { label: string; value: string }) {
	return (
		<div className="admin-detail">
			<span>{label}</span>
			<code>{value}</code>
		</div>
	);
}

function StorageCard({
	title,
	value,
	detail,
}: {
	title: string;
	value: number;
	detail: string;
}) {
	return (
		<article className="storage-card">
			<span>{title}</span>
			<strong>{value}</strong>
			<small>{detail}</small>
		</article>
	);
}
