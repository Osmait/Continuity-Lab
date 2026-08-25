import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
	api,
	browseURL,
	repositoryPath,
	type RepositoryInfo,
	type RepositoryRefs,
} from "./api";
import { CodeBrowser } from "./CodeBrowser";
import { CommitsView } from "./CommitsView";
import { EmptyState, ErrorBanner, Loading, Page } from "./components";
import { BranchIcon, CodeIcon, CopyIcon, HistoryIcon, RepoIcon } from "./icons";
import { shortOID } from "./utils";

type Tab = "code" | "commits";

export function RepoPage() {
	const route = useParams();
	const name = route["*"] ?? "";
	const [parameters, setParameters] = useSearchParams();
	const [info, setInfo] = useState<RepositoryInfo | null>(null);
	const [refs, setRefs] = useState<RepositoryRefs | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<unknown>(null);
	const [copied, setCopied] = useState(false);

	const load = useCallback(async () => {
		setLoading(true);
		setError(null);
		try {
			const [repository, repositoryRefs] = await Promise.all([
				api<RepositoryInfo>(`/api/v1/repos/${repositoryPath(name)}`),
				api<RepositoryRefs>(browseURL(name, { view: "refs" })),
			]);
			setInfo(repository);
			setRefs(repositoryRefs);
		} catch (caught) {
			setError(caught);
		} finally {
			setLoading(false);
		}
	}, [name]);

	useEffect(() => {
		void load();
	}, [load]);

	const tab = (parameters.get("tab") as Tab | null) ?? "code";
	const selectedRef =
		parameters.get("ref") ??
		refs?.default_branch ??
		info?.manifest.default_branch ??
		"";
	const directory = parameters.get("path") ?? "";
	const file = parameters.get("file") ?? "";
	const selectedOID = parameters.get("oid") ?? "";

	const updateParameters = useCallback(
		(changes: Record<string, string | null>) => {
			const next = new URLSearchParams(parameters);
			Object.entries(changes).forEach(([key, value]) =>
				value ? next.set(key, value) : next.delete(key),
			);
			setParameters(next);
		},
		[parameters, setParameters],
	);

	const cloneURL = `${window.location.origin}/git/${name}.git`;
	const selected = useMemo(
		() =>
			refs?.branches.find((ref) => ref.name === selectedRef) ??
			refs?.tags.find((ref) => ref.name === selectedRef),
		[refs, selectedRef],
	);

	if (loading)
		return (
			<Page className="repo-page">
				<Loading />
			</Page>
		);
	if (error || !info || !refs) {
		return (
			<Page className="repo-page">
				<ErrorBanner
					error={error ?? new Error("Repository metadata is unavailable")}
					retry={() => void load()}
				/>
			</Page>
		);
	}

	const empty = refs.branches.length === 0 && refs.tags.length === 0;

	async function copyCloneURL() {
		await navigator.clipboard.writeText(cloneURL);
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1500);
	}

	return (
		<Page className="repo-page">
			<div className="repo-shell">
				<Link className="back-link repo-back" to="/">
					← Repositories
				</Link>

				<section className="repo-heading">
					<div>
						<span className="section-kicker">CONTINUITY / REPOSITORY</span>
						<div className="repo-title">
							<RepoIcon width="22" height="22" />
							<h1>{name}</h1>
							<span className="visibility-badge">LAB</span>
						</div>
						<p>
							Browse branches, source files, README documents, and commit
							history.
						</p>
					</div>
					<div className="repo-heading-actions">
						<div className="branch-select">
							<BranchIcon />
							<select
								value={selectedRef}
								disabled={empty}
								onChange={(event) =>
									updateParameters({
										ref: event.target.value,
										path: null,
										file: null,
										oid: null,
									})
								}
								aria-label="Branch or tag"
							>
								{refs.branches.length > 0 && (
									<optgroup label="Branches">
										{refs.branches.map((ref) => (
											<option value={ref.name} key={ref.name}>
												{ref.short_name}
											</option>
										))}
									</optgroup>
								)}
								{refs.tags.length > 0 && (
									<optgroup label="Tags">
										{refs.tags.map((ref) => (
											<option value={ref.name} key={ref.name}>
												{ref.short_name}
											</option>
										))}
									</optgroup>
								)}
								{empty && (
									<option>
										{info.manifest.default_branch.replace("refs/heads/", "")}
									</option>
								)}
							</select>
						</div>
						<button
							className="button clone-action"
							onClick={() => void copyCloneURL()}
						>
							<CopyIcon />
							{copied ? "Copied" : "Clone"}
						</button>
					</div>
				</section>

				<nav className="repo-tabs" aria-label="Repository navigation">
					<button
						className={tab === "code" ? "active" : ""}
						onClick={() => updateParameters({ tab: "code", oid: null })}
					>
						<CodeIcon />
						Code
					</button>
					<button
						className={tab === "commits" ? "active" : ""}
						onClick={() =>
							updateParameters({ tab: "commits", file: null, path: null })
						}
					>
						<HistoryIcon />
						Commits
					</button>
				</nav>

				{selected && (
					<div className="tip-strip">
						<span className="tip-avatar">C</span>
						<strong>{selected.subject || "Current branch tip"}</strong>
						<code>{shortOID(selected.oid, 10)}</code>
						<span>{selected.short_name}</span>
					</div>
				)}

				<section
					className={`repo-content ${tab === "code" ? "with-sidebar" : ""}`}
				>
					<div className="repo-primary-view">
						{empty ? (
							<EmptyState
								icon={<RepoIcon width="30" height="30" />}
								title="Quick setup — this repository is empty"
							>
								Push an existing repository with{" "}
								<code>git remote add origin {cloneURL}</code> and{" "}
								<code>git push -u origin main</code>.
							</EmptyState>
						) : tab === "code" ? (
							<CodeBrowser
								name={name}
								revision={selectedRef}
								directory={directory}
								file={file}
								onOpenDirectory={(path) =>
									updateParameters({ path, file: null })
								}
								onOpenFile={(path) => updateParameters({ file: path })}
								onCloseFile={() => updateParameters({ file: null })}
								onCommitted={() => void load()}
							/>
						) : (
							<CommitsView
								name={name}
								revision={selectedRef}
								selectedOID={selectedOID}
								onSelect={(oid) => updateParameters({ oid })}
							/>
						)}
					</div>

					{tab === "code" && (
						<aside className="repo-sidebar">
							<div className="side-card">
								<h3>Clone</h3>
								<button
									className="clone-command"
									onClick={() => void copyCloneURL()}
								>
									<code>{cloneURL}</code>
									<CopyIcon />
								</button>
							</div>
							<div className="side-card">
								<h3>Repository</h3>
								<SideValue
									label="Default branch"
									value={info.manifest.default_branch.replace(
										"refs/heads/",
										"",
									)}
								/>
								<SideValue
									label="Object format"
									value={info.manifest.object_format.toUpperCase()}
								/>
								<SideValue
									label="Branches"
									value={String(refs.branches.length)}
								/>
								<SideValue label="Tags" value={String(refs.tags.length)} />
							</div>
						</aside>
					)}
				</section>
			</div>
		</Page>
	);
}

function SideValue({ label, value }: { label: string; value: string }) {
	return (
		<div className="side-value">
			<span>{label}</span>
			<strong>{value}</strong>
		</div>
	);
}
