import type { Extension } from "@codemirror/state";
import { githubDark } from "@uiw/codemirror-theme-github";
import CodeMirror from "@uiw/react-codemirror";
import { useEffect, useState, type SyntheticEvent } from "react";
import {
	api,
	APIError,
	editURL,
	type EditFileRequest,
	type EditFileResponse,
} from "./api";
import { BranchIcon, FileIcon } from "./icons";
import { formatBytes, shortOID } from "./utils";

const MAX_CONTENT_SIZE = 4 * 1024 * 1024;

interface FileEditorProps {
	repository: string;
	branch: string;
	baseCommit: string;
	path: string;
	content: string;
	create: boolean;
	onCancel: () => void;
	onCommitted: (result: EditFileResponse) => void;
}

export function FileEditor(props: FileEditorProps) {
	const [filePath, setFilePath] = useState(props.path);
	const [content, setContent] = useState(props.content);
	const [message, setMessage] = useState(
		props.create
			? `Create ${props.path || "new file"}`
			: `Update ${props.path}`,
	);
	const [authorName, setAuthorName] = useState(
		() =>
			localStorage.getItem("continuity.editor.name") ?? "Continuity Web Editor",
	);
	const [authorEmail, setAuthorEmail] = useState(
		() =>
			localStorage.getItem("continuity.editor.email") ??
			"web-editor@continuity.local",
	);
	const [saving, setSaving] = useState(false);
	const [error, setError] = useState("");
	const [extensions, setExtensions] = useState<Extension[]>([]);
	useEffect(() => {
		let active = true;
		void languageExtensions(filePath).then(
			(loaded) => active && setExtensions(loaded),
		);
		return () => {
			active = false;
		};
	}, [filePath]);
	const pathError = validateEditorPath(filePath);
	const contentTooLarge = new Blob([content]).size > MAX_CONTENT_SIZE;

	async function submit(event: SyntheticEvent<HTMLFormElement>) {
		event.preventDefault();
		if (pathError || contentTooLarge || !message.trim()) return;
		setSaving(true);
		setError("");
		localStorage.setItem("continuity.editor.name", authorName);
		localStorage.setItem("continuity.editor.email", authorEmail);
		const input: EditFileRequest = {
			branch: props.branch,
			path: filePath,
			content,
			base_commit: props.baseCommit,
			commit_message: message.trim(),
			author_name: authorName.trim(),
			author_email: authorEmail.trim(),
			create: props.create,
		};
		try {
			const result = await api<EditFileResponse>(editURL(props.repository), {
				method: "POST",
				body: JSON.stringify(input),
			});
			props.onCommitted(result);
		} catch (caught) {
			if (caught instanceof APIError && caught.status === 409) {
				setError(
					"The branch changed while you were editing. Reload the latest version before committing again.",
				);
			} else {
				setError(
					caught instanceof Error
						? caught.message
						: "Unable to publish the commit.",
				);
			}
		} finally {
			setSaving(false);
		}
	}

	return (
		<form className="file-editor" onSubmit={(event) => void submit(event)}>
			<header className="editor-header">
				<div>
					<FileIcon />
					<strong>
						{props.create ? "Create new file" : `Edit ${props.path}`}
					</strong>
				</div>
				<div className="editor-base">
					<BranchIcon />
					{props.branch.replace("refs/heads/", "")}
					<code>{shortOID(props.baseCommit)}</code>
				</div>
			</header>

			{props.create && (
				<div className="editor-path-field">
					<label htmlFor="editor-file-path">File path</label>
					<input
						id="editor-file-path"
						value={filePath}
						onChange={(event) => {
							setFilePath(event.target.value);
							if (!message || message.startsWith("Create "))
								setMessage(`Create ${event.target.value || "new file"}`);
						}}
						placeholder="path/to/file.txt"
						autoFocus
					/>
					{pathError && <span>{pathError}</span>}
				</div>
			)}

			<div className="editor-toolbar">
				<span>EDIT FILE</span>
				<span>{formatBytes(new Blob([content]).size)} / 4 MB</span>
			</div>
			<div className="editor-codemirror">
				<CodeMirror
					value={content}
					height="52vh"
					minHeight="340px"
					theme={githubDark}
					extensions={extensions}
					onChange={setContent}
					basicSetup={{
						lineNumbers: true,
						foldGutter: true,
						highlightActiveLine: true,
						highlightActiveLineGutter: true,
					}}
				/>
			</div>
			{contentTooLarge && (
				<p className="editor-error">
					File content exceeds the 4 MB web editor limit.
				</p>
			)}

			<section className="commit-form">
				<div className="commit-form-heading">
					<div className="commit-avatar">W</div>
					<div>
						<strong>Commit changes</strong>
						<span>
							This creates a real Git commit and pushes it directly to the
							selected branch.
						</span>
					</div>
				</div>
				<label htmlFor="editor-commit-message">Commit message</label>
				<input
					id="editor-commit-message"
					value={message}
					onChange={(event) => setMessage(event.target.value)}
					maxLength={4000}
					required
				/>
				<div className="author-fields">
					<label>
						Name
						<input
							value={authorName}
							onChange={(event) => setAuthorName(event.target.value)}
							maxLength={200}
							required
						/>
					</label>
					<label>
						Email
						<input
							type="email"
							value={authorEmail}
							onChange={(event) => setAuthorEmail(event.target.value)}
							maxLength={254}
							required
						/>
					</label>
				</div>
				{error && (
					<p className="editor-error" role="alert">
						{error}
					</p>
				)}
				<div className="commit-actions">
					<button
						type="button"
						className="button"
						onClick={props.onCancel}
						disabled={saving}
					>
						Cancel
					</button>
					<button
						className="button primary"
						disabled={
							saving || !!pathError || contentTooLarge || !message.trim()
						}
					>
						{saving
							? "Publishing commit…"
							: `Commit to ${props.branch.replace("refs/heads/", "")}`}
					</button>
				</div>
			</section>
		</form>
	);
}

function validateEditorPath(value: string): string {
	if (!value) return "A file path is required.";
	if (value.startsWith("/") || value.includes("\\") || value.length > 1024)
		return "Use a relative repository path.";
	const segments = value.split("/");
	if (
		segments.some(
			(segment) =>
				!segment ||
				segment === "." ||
				segment === ".." ||
				segment.toLowerCase() === ".git",
		)
	)
		return "The path contains an unsafe segment.";
	return "";
}

async function languageExtensions(filePath: string): Promise<Extension[]> {
	const extension = filePath.split(".").pop()?.toLowerCase();
	switch (extension) {
		case "js":
		case "jsx": {
			const { javascript } = await import("@codemirror/lang-javascript");
			return [javascript({ jsx: true })];
		}
		case "ts":
		case "tsx": {
			const { javascript } = await import("@codemirror/lang-javascript");
			return [javascript({ jsx: extension === "tsx", typescript: true })];
		}
		case "json": {
			const { json } = await import("@codemirror/lang-json");
			return [json()];
		}
		case "md":
		case "mdx": {
			const { markdown } = await import("@codemirror/lang-markdown");
			return [markdown()];
		}
		case "css":
		case "scss": {
			const { css } = await import("@codemirror/lang-css");
			return [css()];
		}
		case "html":
		case "htm": {
			const { html } = await import("@codemirror/lang-html");
			return [html()];
		}
		case "py": {
			const { python } = await import("@codemirror/lang-python");
			return [python()];
		}
		case "go": {
			const { go } = await import("@codemirror/lang-go");
			return [go()];
		}
		default:
			return [];
	}
}
