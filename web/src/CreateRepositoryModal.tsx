import { useState, type SyntheticEvent } from "react";
import { api } from "./api";
import { RepoIcon } from "./icons";

interface CreateRepositoryModalProps {
	onClose: () => void;
	onCreated: (name: string) => void;
}

export function CreateRepositoryModal({
	onClose,
	onCreated,
}: CreateRepositoryModalProps) {
	const [name, setName] = useState("");
	const [creating, setCreating] = useState(false);
	const [error, setError] = useState("");

	async function submit(event: SyntheticEvent<HTMLFormElement, SubmitEvent>) {
		event.preventDefault();
		const canonical = name.trim();
		if (!canonical) return;
		setCreating(true);
		setError("");
		try {
			await api("/api/v1/repos", {
				method: "POST",
				body: JSON.stringify({ name: canonical, default_branch: "main" }),
			});
			onCreated(canonical);
		} catch (caught) {
			setError(
				caught instanceof Error
					? caught.message
					: "Unable to create repository",
			);
		} finally {
			setCreating(false);
		}
	}

	return (
		<div
			className="modal-backdrop"
			onMouseDown={(event) => {
				if (event.target === event.currentTarget) onClose();
			}}
		>
			<section
				className="modal-card"
				role="dialog"
				aria-modal="true"
				aria-labelledby="create-repository-title"
			>
				<div className="modal-heading">
					<span className="modal-icon">
						<RepoIcon />
					</span>
					<div>
						<h2 id="create-repository-title">Create repository</h2>
						<p>
							Start with an empty main branch backed by the authoritative WAL.
						</p>
					</div>
					<button className="modal-close" onClick={onClose} aria-label="Close">
						×
					</button>
				</div>
				<form onSubmit={(event) => void submit(event)}>
					<label htmlFor="new-repository-name">Repository name</label>
					<div className="repository-name-input">
						<span>continuity /</span>
						<input
							id="new-repository-name"
							autoFocus
							value={name}
							onChange={(event) => setName(event.target.value)}
							placeholder="team/project"
							pattern="[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*"
						/>
					</div>
					<p className="field-help">
						Letters, numbers, dots, dashes, underscores and slashes.
					</p>
					{error && <p className="form-error">{error}</p>}
					<div className="modal-actions">
						<button type="button" className="button" onClick={onClose}>
							Cancel
						</button>
						<button
							className="button primary"
							disabled={creating || !name.trim()}
						>
							{creating ? "Creating…" : "Create repository"}
						</button>
					</div>
				</form>
			</section>
		</div>
	);
}
