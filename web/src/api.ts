export interface RepositorySummary {
	repo_id: string;
	name: string;
	default_branch: string;
	sequence: number;
	ref_count: number;
	updated_at: string;
}

export interface Manifest {
	repo_id: string;
	name: string;
	default_branch: string;
	object_format: string;
	created_at: string;
}

export interface Head {
	revision: number;
	sequence: number;
	refs: Record<string, string>;
	updated_at: string;
	snapshot: null | {
		snapshot_id: string;
		sequence: number;
	};
}

export interface RepositoryInfo {
	manifest: Manifest;
	head: Head;
	head_etag: string;
}

export interface GitRef {
	name: string;
	short_name: string;
	oid: string;
	object_type: string;
	updated_at?: string;
	subject?: string;
}

export interface RepositoryRefs {
	default_branch: string;
	branches: GitRef[];
	tags: GitRef[];
}

export interface TreeEntry {
	name: string;
	path: string;
	mode: string;
	type: "tree" | "blob" | "commit";
	oid: string;
	size?: number;
}

export interface TreeResponse {
	revision: string;
	commit: string;
	path: string;
	entries: TreeEntry[];
}

export interface BlobResponse {
	revision: string;
	commit: string;
	path: string;
	oid: string;
	size: number;
	encoding: "utf-8" | "base64";
	content: string;
	binary: boolean;
	truncated: boolean;
}

export interface Commit {
	oid: string;
	parents: string[];
	author_name: string;
	author_email: string;
	authored_at: string;
	subject: string;
}

export interface CommitDetail extends Commit {
	body: string;
	changes: Array<{ status: string; path: string }>;
}

export interface WALEntry {
	entry_id: string;
	push_id: string;
	sequence: number;
	created_at: string;
	node_id: string;
	updates: Array<{
		ref: string;
		old_oid: string;
		new_oid: string;
		force: boolean;
	}>;
	payload?: { pack_size: number; pack_sha256: string };
}

export interface WALResponse {
	repo_id: string;
	sequence: number;
	entries_newest_first: WALEntry[];
}

export interface EditFileRequest {
	branch: string;
	path: string;
	content: string;
	base_commit: string;
	commit_message: string;
	author_name: string;
	author_email: string;
	create: boolean;
}

export interface EditFileResponse {
	commit_oid: string;
	branch: string;
	path: string;
	created: boolean;
	sequence?: number;
}

export interface ClusterResponse {
	nodes: Array<{
		node: { id: string; url: string };
		healthy: boolean;
		checked_at: string;
	}>;
}

export class APIError extends Error {
	status: number;

	constructor(status: number, message: string) {
		super(message);
		this.status = status;
	}
}

export const repositoryPath = (name: string) =>
	name.split("/").map(encodeURIComponent).join("/");

export async function api<T>(url: string, init?: RequestInit): Promise<T> {
	const response = await fetch(url, {
		...init,
		headers: {
			Accept: "application/json",
			...(init?.body ? { "Content-Type": "application/json" } : {}),
			...init?.headers,
		},
	});
	if (!response.ok) {
		const body = await response.text();
		let message = body.trim() || `${response.status} ${response.statusText}`;
		try {
			const parsed = JSON.parse(body) as { error?: { message?: string } };
			message = parsed.error?.message ?? message;
		} catch {
			// Plain-text node errors are already useful.
		}
		throw new APIError(response.status, message);
	}
	return response.json() as Promise<T>;
}

export const editURL = (name: string) => `/api/v1/edit/${repositoryPath(name)}`;

export const browseURL = (
	name: string,
	query: Record<string, string | number | undefined>,
) => {
	const parameters = new URLSearchParams();
	Object.entries(query).forEach(([key, value]) => {
		if (value !== undefined && value !== "") parameters.set(key, String(value));
	});
	return `/api/v1/browse/${repositoryPath(name)}?${parameters}`;
};
