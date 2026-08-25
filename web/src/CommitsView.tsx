import { useEffect, useState } from "react";
import { api, browseURL, type Commit, type CommitDetail } from "./api";
import { Avatar, EmptyState, ErrorBanner, Loading } from "./components";
import { FileIcon, HistoryIcon } from "./icons";
import { avatarColor, relativeTime, shortOID } from "./utils";

export function CommitsView({
	name,
	revision,
	selectedOID,
	onSelect,
}: {
	name: string;
	revision: string;
	selectedOID: string;
	onSelect: (oid: string) => void;
}) {
	if (selectedOID)
		return (
			<CommitDetails
				name={name}
				oid={selectedOID}
				onBack={() => onSelect("")}
			/>
		);
	return <CommitList name={name} revision={revision} onSelect={onSelect} />;
}

function CommitList({
	name,
	revision,
	onSelect,
}: {
	name: string;
	revision: string;
	onSelect: (oid: string) => void;
}) {
	const [commits, setCommits] = useState<Commit[]>([]);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<unknown>(null);

	useEffect(() => {
		let active = true;
		setLoading(true);
		setError(null);
		void api<Commit[]>(
			browseURL(name, { view: "commits", ref: revision, limit: 100 }),
		)
			.then((result) => active && setCommits(result))
			.catch((caught) => active && setError(caught))
			.finally(() => active && setLoading(false));
		return () => {
			active = false;
		};
	}, [name, revision]);

	if (loading) return <Loading label="Loading commit history…" />;
	if (error) return <ErrorBanner error={error} />;
	if (!commits.length)
		return (
			<EmptyState
				icon={<HistoryIcon width="28" height="28" />}
				title="No commits yet"
			>
				Push the first commit to this branch.
			</EmptyState>
		);

	const grouped = groupByDate(commits);
	return (
		<div className="commit-timeline">
			{grouped.map(([date, entries]) => (
				<section className="commit-group" key={date}>
					<h3>Commits on {date}</h3>
					<div className="commit-list">
						{entries.map((commit) => (
							<button
								className="commit-row"
								key={commit.oid}
								onClick={() => onSelect(commit.oid)}
							>
								<Avatar
									name={commit.author_name}
									color={avatarColor(commit.author_email)}
								/>
								<span className="commit-main">
									<strong>{commit.subject}</strong>
									<small>
										{commit.author_name} committed{" "}
										{relativeTime(commit.authored_at)}
									</small>
								</span>
								<code>{shortOID(commit.oid)}</code>
								<span className="row-arrow">›</span>
							</button>
						))}
					</div>
				</section>
			))}
		</div>
	);
}

function CommitDetails({
	name,
	oid,
	onBack,
}: {
	name: string;
	oid: string;
	onBack: () => void;
}) {
	const [detail, setDetail] = useState<CommitDetail | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<unknown>(null);

	useEffect(() => {
		let active = true;
		setLoading(true);
		setError(null);
		void api<CommitDetail>(browseURL(name, { view: "commit", oid }))
			.then((result) => active && setDetail(result))
			.catch((caught) => active && setError(caught))
			.finally(() => active && setLoading(false));
		return () => {
			active = false;
		};
	}, [name, oid]);

	if (loading) return <Loading label="Loading commit…" />;
	if (error) return <ErrorBanner error={error} />;
	if (!detail) return null;

	return (
		<div className="commit-detail">
			<button className="back-link" onClick={onBack}>
				← All commits
			</button>
			<section className="commit-card">
				<div className="commit-title">
					<h2>{detail.subject}</h2>
					<code>{detail.oid}</code>
				</div>
				{detail.body && detail.body !== detail.subject && (
					<pre className="commit-body">{detail.body}</pre>
				)}
				<div className="commit-author">
					<Avatar
						name={detail.author_name}
						color={avatarColor(detail.author_email)}
					/>
					<span>
						<strong>{detail.author_name}</strong>
						<small>
							{detail.author_email} ·{" "}
							{new Date(detail.authored_at).toLocaleString()}
						</small>
					</span>
				</div>
				<div className="commit-parents">
					<span>
						{detail.parents.length} parent
						{detail.parents.length === 1 ? "" : "s"}
					</span>
					{detail.parents.map((parent) => (
						<code key={parent}>{shortOID(parent)}</code>
					))}
				</div>
			</section>
			<section className="changes-card">
				<div className="changes-header">
					<strong>
						{detail.changes.length} changed file
						{detail.changes.length === 1 ? "" : "s"}
					</strong>
				</div>
				{detail.changes.map((change) => (
					<div className="change-row" key={`${change.status}-${change.path}`}>
						<span
							className={`change-status status-${change.status.toLowerCase()}`}
						>
							{change.status}
						</span>
						<FileIcon />
						<span>{change.path}</span>
					</div>
				))}
			</section>
		</div>
	);
}

function groupByDate(commits: Commit[]): Array<[string, Commit[]]> {
	const groups = new Map<string, Commit[]>();
	commits.forEach((commit) => {
		const date = new Date(commit.authored_at).toLocaleDateString("en", {
			year: "numeric",
			month: "long",
			day: "numeric",
		});
		const entries = groups.get(date) ?? [];
		entries.push(commit);
		groups.set(date, entries);
	});
	return [...groups.entries()];
}
