import { lazy, Suspense, useEffect, useState } from "react";
import ReactMarkdown from "react-markdown";
import {
	api,
	browseURL,
	type BlobResponse,
	type TreeEntry,
	type TreeResponse,
} from "./api";
import { EmptyState, ErrorBanner, Loading } from "./components";
import { FileIcon, FolderIcon } from "./icons";
import { formatBytes, languageFor, shortOID } from "./utils";

const FileEditor = lazy(() =>
	import("./FileEditor").then((module) => ({ default: module.FileEditor })),
);

interface CodeBrowserProps {
	name: string;
	revision: string;
	directory: string;
	file: string;
	onOpenDirectory: (path: string) => void;
	onOpenFile: (path: string) => void;
	onCloseFile: () => void;
	onCommitted: () => void;
}

interface EditorState {
	path: string;
	content: string;
	baseCommit: string;
	create: boolean;
}

interface BrowserViewProps extends CodeBrowserProps {
	reloadToken: number;
	onCreate?: (baseCommit: string, directory: string) => void;
	onEdit?: (blob: BlobResponse) => void;
}

export function CodeBrowser(props: CodeBrowserProps) {
	const [editor, setEditor] = useState<EditorState | null>(null);
	const [reloadToken, setReloadToken] = useState(0);
	const editable = props.revision.startsWith("refs/heads/");

	if (editor) {
		return (
			<Suspense fallback={<Loading label="Loading web editor…" />}>
				<FileEditor
					repository={props.name}
					branch={props.revision}
					baseCommit={editor.baseCommit}
					path={editor.path}
					content={editor.content}
					create={editor.create}
					onCancel={() => setEditor(null)}
					onCommitted={(result) => {
						setEditor(null);
						setReloadToken((value) => value + 1);
						props.onOpenFile(result.path);
						props.onCommitted();
					}}
				/>
			</Suspense>
		);
	}

	if (props.file) {
		return (
			<BlobViewer
				{...props}
				reloadToken={reloadToken}
				onEdit={
					editable
						? (blob) =>
								setEditor({
									path: blob.path,
									content: blob.content,
									baseCommit: blob.commit,
									create: false,
								})
						: undefined
				}
			/>
		);
	}
	return (
		<TreeViewer
			{...props}
			reloadToken={reloadToken}
			onCreate={
				editable
					? (baseCommit, directory) =>
							setEditor({
								path: directory ? `${directory}/` : "",
								content: "",
								baseCommit,
								create: true,
							})
					: undefined
			}
		/>
	);
}

function TreeViewer({
	name,
	revision,
	directory,
	onOpenDirectory,
	onOpenFile,
	onCreate,
	reloadToken,
}: BrowserViewProps) {
	const [tree, setTree] = useState<TreeResponse | null>(null);
	const [readme, setReadme] = useState<BlobResponse | null>(null);
	const [error, setError] = useState<unknown>(null);
	const [loading, setLoading] = useState(true);
	const reloadKey = `${name}:${revision}:${directory}:${reloadToken}`;

	useEffect(() => {
		let active = true;
		setLoading(true);
		setError(null);
		setReadme(null);
		void api<TreeResponse>(
			browseURL(name, { view: "tree", ref: revision, path: directory }),
		)
			.then(async (result) => {
				if (!active) return;
				setTree(result);
				const candidate = result.entries.find(
					(entry) =>
						entry.type === "blob" && /^readme(?:\.[^.]+)?$/i.test(entry.name),
				);
				if (candidate) {
					try {
						const blob = await api<BlobResponse>(
							browseURL(name, {
								view: "blob",
								ref: revision,
								path: candidate.path,
							}),
						);
						if (active && !blob.binary && !blob.truncated) setReadme(blob);
					} catch {
						// README preview is optional; the tree remains useful.
					}
				}
			})
			.catch((caught) => active && setError(caught))
			.finally(() => active && setLoading(false));
		return () => {
			active = false;
		};
	}, [reloadKey, name, revision, directory]);

	if (loading) return <Loading label="Loading repository tree…" />;
	if (error) return <ErrorBanner error={error} />;
	if (!tree) return null;

	const directories = tree.entries.filter((entry) => entry.type === "tree");
	const files = tree.entries.filter((entry) => entry.type !== "tree");
	const ordered = [...directories, ...files];

	return (
		<>
			<div className="tree-card">
				<div className="tree-head">
					<span>
						<strong>{shortOID(tree.commit)}</strong> at{" "}
						{directory || "repository root"}
					</span>
					<div className="tree-head-actions">
						<span>{tree.entries.length} items</span>
						{onCreate && (
							<button
								className="button small"
								onClick={() => onCreate(tree.commit, directory)}
							>
								Create file
							</button>
						)}
					</div>
				</div>
				<div className="tree-rows">
					{directory && (
						<button
							className="tree-row"
							onClick={() => onOpenDirectory(parentPath(directory))}
						>
							<FolderIcon className="folder" />
							<span className="tree-name">..</span>
							<span className="tree-description">Parent directory</span>
						</button>
					)}
					{ordered.map((entry) => (
						<TreeRow
							key={entry.oid + entry.path}
							entry={entry}
							onDirectory={onOpenDirectory}
							onFile={onOpenFile}
						/>
					))}
				</div>
			</div>
			{readme && (
				<section className="readme-card">
					<div className="readme-header">
						<FileIcon />
						<strong>{readme.path}</strong>
					</div>
					<article className="markdown-body">
						<ReactMarkdown>{readme.content}</ReactMarkdown>
					</article>
				</section>
			)}
		</>
	);
}

function TreeRow({
	entry,
	onDirectory,
	onFile,
}: {
	entry: TreeEntry;
	onDirectory: (path: string) => void;
	onFile: (path: string) => void;
}) {
	const isDirectory = entry.type === "tree";
	return (
		<button
			className="tree-row"
			onClick={() =>
				isDirectory ? onDirectory(entry.path) : onFile(entry.path)
			}
		>
			{isDirectory ? (
				<FolderIcon className="folder" />
			) : (
				<FileIcon className="file" />
			)}
			<span className="tree-name">{entry.name}</span>
			<span className="tree-description">
				{isDirectory ? "Directory" : languageFor(entry.name)}
			</span>
			<span className="tree-size">
				{isDirectory ? "" : formatBytes(entry.size)}
			</span>
		</button>
	);
}

function BlobViewer({
	name,
	revision,
	file,
	onCloseFile,
	onOpenDirectory,
	onEdit,
	reloadToken,
}: BrowserViewProps) {
	const [blob, setBlob] = useState<BlobResponse | null>(null);
	const [error, setError] = useState<unknown>(null);
	const [loading, setLoading] = useState(true);

	useEffect(() => {
		let active = true;
		setLoading(true);
		setError(null);
		void api<BlobResponse>(
			browseURL(name, { view: "blob", ref: revision, path: file }),
		)
			.then((result) => active && setBlob(result))
			.catch((caught) => active && setError(caught))
			.finally(() => active && setLoading(false));
		return () => {
			active = false;
		};
	}, [name, revision, file, reloadToken]);

	if (loading) return <Loading label="Loading file…" />;
	if (error) return <ErrorBanner error={error} />;
	if (!blob) return null;

	const segments = file.split("/");
	return (
		<div className="blob-view">
			<div className="path-bar">
				<button
					onClick={() => {
						onCloseFile();
						onOpenDirectory("");
					}}
				>
					{name}
				</button>
				{segments.map((segment, index) => {
					const target = segments.slice(0, index + 1).join("/");
					const last = index === segments.length - 1;
					return (
						<span key={target}>
							/
							<button
								className={last ? "current" : ""}
								onClick={() =>
									last ? undefined : (onCloseFile(), onOpenDirectory(target))
								}
							>
								{segment}
							</button>
						</span>
					);
				})}
			</div>
			<div className="blob-card">
				<div className="blob-header">
					<div>
						<FileIcon />
						<strong>{segments.at(-1)}</strong>
						<span>{formatBytes(blob.size)}</span>
					</div>
					<div>
						<code>{shortOID(blob.oid, 10)}</code>
						{onEdit && !blob.binary && !blob.truncated && (
							<button
								className="button small primary"
								onClick={() => onEdit(blob)}
							>
								Edit file
							</button>
						)}
						<button className="button small" onClick={onCloseFile}>
							Back to files
						</button>
					</div>
				</div>
				{blob.truncated ? (
					<EmptyState
						icon={<FileIcon width="28" height="28" />}
						title="File is too large to display"
					>
						The browser preview is limited to 4 MB.
					</EmptyState>
				) : blob.binary ? (
					<EmptyState
						icon={<FileIcon width="28" height="28" />}
						title="Binary file"
					>
						Binary content is not rendered in the code explorer.
					</EmptyState>
				) : (
					<div
						className="code-scroll"
						role="region"
						aria-label={`Contents of ${file}`}
					>
						<div className="code-lines">
							{blob.content.split("\n").map((line, index) => (
								<div
									className="code-line"
									key={`${index}-${line.slice(0, 16)}`}
								>
									<span className="line-number" aria-hidden="true">
										{index + 1}
									</span>
									<code>{line || " "}</code>
								</div>
							))}
						</div>
					</div>
				)}
			</div>
		</div>
	);
}

function parentPath(value: string) {
	const segments = value.split("/");
	segments.pop();
	return segments.join("/");
}
