import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { api, type RepositorySummary } from "./api";
import { CodeIcon, SearchIcon } from "./icons";

export function AppHeader() {
	const location = useLocation();
	const navigate = useNavigate();
	const inputRef = useRef<HTMLInputElement>(null);
	const [query, setQuery] = useState("");
	const [repositories, setRepositories] = useState<RepositorySummary[]>([]);

	useEffect(() => {
		void api<{ repositories: RepositorySummary[] }>("/api/v1/repos")
			.then((result) => setRepositories(result.repositories))
			.catch(() => undefined);
	}, []);

	useEffect(() => {
		function focusSearch(event: KeyboardEvent) {
			const target = event.target as HTMLElement | null;
			if (
				event.key === "/" &&
				target?.tagName !== "INPUT" &&
				target?.tagName !== "TEXTAREA"
			) {
				event.preventDefault();
				inputRef.current?.focus();
			}
		}
		document.addEventListener("keydown", focusSearch);
		return () => document.removeEventListener("keydown", focusSearch);
	}, []);

	const suggestions = query
		? repositories
				.filter((repo) => repo.name.toLowerCase().includes(query.toLowerCase()))
				.slice(0, 6)
		: [];
	const workspace = location.pathname.startsWith("/repos/")
		? decodeURIComponent(location.pathname.replace("/repos/", ""))
		: "repositories";

	return (
		<header className="app-header">
			<div className="header-inner">
				<div className="header-identity">
					<Link className="brand" to="/" aria-label="Continuity home">
						<span className="brand-mark">
							<CodeIcon width="14" height="14" />
						</span>
						<span>Continuity</span>
					</Link>
					<span className="identity-separator">/</span>
					<span className="workspace-name">{workspace}</span>
				</div>

				<div className="global-search">
					<SearchIcon className="search-icon" />
					<input
						ref={inputRef}
						value={query}
						onChange={(event) => setQuery(event.target.value)}
						onKeyDown={(event) => {
							if (event.key === "Enter" && suggestions[0]) {
								navigate(`/repos/${suggestions[0].name}`);
								setQuery("");
							}
						}}
						placeholder="Search repositories"
						aria-label="Search repositories"
					/>
					<span className="search-shortcut">/</span>
					{suggestions.length > 0 && (
						<div className="search-results">
							{suggestions.map((repo) => (
								<Link
									key={repo.repo_id}
									to={`/repos/${repo.name}`}
									onClick={() => setQuery("")}
								>
									<CodeIcon />
									<span>{repo.name}</span>
									<code>{repo.repo_id.slice(0, 7)}</code>
								</Link>
							))}
						</div>
					)}
				</div>

				<div className="header-actions">
					<Link className="header-link admin-console-link" to="/admin">
						Admin console
					</Link>
					<button className="header-create" onClick={() => navigate("/?new=1")}>
						New repository
					</button>
				</div>
			</div>
		</header>
	);
}

export function Page({
	children,
	className = "",
}: {
	children: ReactNode;
	className?: string;
}) {
	return <main className={`page ${className}`}>{children}</main>;
}

export function Loading({ label = "Loading repository…" }: { label?: string }) {
	return (
		<div className="loading">
			<span className="spinner" />
			{label}
		</div>
	);
}

export function ErrorBanner({
	error,
	retry,
}: {
	error: unknown;
	retry?: () => void;
}) {
	const message =
		error instanceof Error ? error.message : "An unexpected error occurred.";
	return (
		<div className="error-banner" role="alert">
			<div>
				<strong>Unable to load this view</strong>
				<span>{message}</span>
			</div>
			{retry && (
				<button className="button" onClick={retry}>
					Try again
				</button>
			)}
		</div>
	);
}

export function EmptyState({
	icon,
	title,
	children,
}: {
	icon: ReactNode;
	title: string;
	children: ReactNode;
}) {
	return (
		<div className="empty-state">
			<div className="empty-icon">{icon}</div>
			<h2>{title}</h2>
			<p>{children}</p>
		</div>
	);
}

export function Avatar({ name, color }: { name: string; color: string }) {
	return (
		<span className="avatar" style={{ backgroundColor: color }}>
			{name.slice(0, 1).toUpperCase()}
		</span>
	);
}
