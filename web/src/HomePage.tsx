import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, type RepositorySummary } from "./api";
import { EmptyState, ErrorBanner, Loading, Page } from "./components";
import { CreateRepositoryModal } from "./CreateRepositoryModal";
import { BranchIcon, RepoIcon, SearchIcon } from "./icons";
import { relativeTime } from "./utils";

type RepositoryFilter = "all" | "chaos" | "concurrency" | "showcase";

const filters: Array<{ id: RepositoryFilter; label: string }> = [
	{ id: "all", label: "All" },
	{ id: "showcase", label: "Showcase" },
	{ id: "chaos", label: "Chaos" },
	{ id: "concurrency", label: "Concurrency" },
];

export function HomePage() {
	const navigate = useNavigate();
	const [parameters, setParameters] = useSearchParams();
	const [repositories, setRepositories] = useState<RepositorySummary[]>([]);
	const [query, setQuery] = useState("");
	const [filter, setFilter] = useState<RepositoryFilter>("all");
	const [error, setError] = useState<unknown>(null);
	const [loading, setLoading] = useState(true);

	const load = useCallback(async () => {
		setLoading(true);
		setError(null);
		try {
			const repoResult = await api<{ repositories: RepositorySummary[] }>(
				"/api/v1/repos",
			);
			setRepositories(repoResult.repositories);
		} catch (caught) {
			setError(caught);
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		void load();
	}, [load]);

	const filtered = useMemo(() => {
		const normalized = query.trim().toLowerCase();
		return repositories.filter((repo) => {
			const matchesText =
				!normalized || repo.name.toLowerCase().includes(normalized);
			const matchesCategory =
				filter === "all" || repo.name.startsWith(`${filter}/`);
			return matchesText && matchesCategory;
		});
	}, [filter, query, repositories]);

	const totalRefs = repositories.reduce((sum, repo) => sum + repo.ref_count, 0);
	const repositoriesWithHistory = repositories.filter(
		(repo) => repo.sequence > 0,
	).length;
	const showCreate = parameters.get("new") === "1";

	function closeCreate() {
		const next = new URLSearchParams(parameters);
		next.delete("new");
		setParameters(next, { replace: true });
	}

	return (
		<Page className="home-page">
			<section className="home-intro">
				<div>
					<span className="section-kicker">CONTINUITY / GIT</span>
					<h1>Repositories</h1>
					<p>
						Browse code, inspect history, and follow every durable change
						published through MinIO.
					</p>
				</div>
				<button
					className="button primary desktop-create"
					onClick={() => setParameters({ new: "1" })}
				>
					New repository
				</button>
			</section>

			<section className="metric-strip git-metrics" aria-label="Git overview">
				<Metric
					icon={<RepoIcon />}
					label="Repositories"
					value={repositories.length}
				/>
				<Metric
					icon={<BranchIcon />}
					label="Published refs"
					value={totalRefs}
				/>
				<Metric
					icon={<RepoIcon />}
					label="With history"
					value={repositoriesWithHistory}
				/>
			</section>

			<section className="repository-browser">
				<div className="repository-controls">
					<div
						className="filter-tabs"
						role="tablist"
						aria-label="Repository groups"
					>
						{filters.map((item) => (
							<button
								key={item.id}
								className={filter === item.id ? "active" : ""}
								onClick={() => setFilter(item.id)}
							>
								{item.label}
							</button>
						))}
					</div>
					<label className="inline-search">
						<SearchIcon />
						<input
							value={query}
							onChange={(event) => setQuery(event.target.value)}
							placeholder="Filter repositories"
							aria-label="Filter repositories"
						/>
					</label>
				</div>

				<div className="repository-table-head" aria-hidden="true">
					<span>Repository</span>
					<span>Updated</span>
					<span>Refs</span>
					<span>Default branch</span>
					<span />
				</div>
				{loading ? (
					<Loading label="Loading repositories…" />
				) : error ? (
					<ErrorBanner error={error} retry={() => void load()} />
				) : filtered.length === 0 ? (
					<EmptyState
						icon={<RepoIcon width="26" height="26" />}
						title={
							repositories.length
								? "No matching repositories"
								: "No repositories yet"
						}
					>
						{repositories.length
							? "Change the active filter or search term."
							: "Create a repository to publish your first durable Git history."}
					</EmptyState>
				) : (
					<div className="repo-list">
						{filtered.map((repo) => (
							<Link
								className="repo-row"
								to={`/repos/${repo.name}`}
								key={repo.repo_id}
							>
								<span className="repo-summary">
									<strong>{repo.name}</strong>
									<small>{repo.repo_id.slice(0, 12)}</small>
								</span>
								<span className="repo-updated">
									{relativeTime(repo.updated_at)}
								</span>
								<span className="repo-meta">
									<BranchIcon /> {repo.ref_count}
								</span>
								<span className="repo-default-branch">
									{repo.default_branch.replace("refs/heads/", "")}
								</span>
								<span className="row-arrow">→</span>
							</Link>
						))}
					</div>
				)}
			</section>

			<div className="home-footnote">
				<span>MinIO authoritative</span>
				<span>Conditional reads</span>
				<span>Disposable Git nodes</span>
			</div>

			{showCreate && (
				<CreateRepositoryModal
					onClose={closeCreate}
					onCreated={(name) => {
						closeCreate();
						navigate(`/repos/${name}`);
					}}
				/>
			)}
		</Page>
	);
}

function Metric({
	icon,
	label,
	value,
	mutedSuffix,
}: {
	icon: React.ReactNode;
	label: string;
	value: string | number;
	mutedSuffix?: string;
}) {
	return (
		<div className="metric-cell">
			<div className="metric-label">
				{icon}
				<span>{label}</span>
			</div>
			<div className="metric-value">
				{value}
				{mutedSuffix && <small>{mutedSuffix}</small>}
			</div>
		</div>
	);
}
