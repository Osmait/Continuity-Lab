export function shortOID(oid: string, length = 7) {
	return oid.slice(0, length);
}

export function formatBytes(bytes = 0) {
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function relativeTime(value: string) {
	if (!value || value.startsWith("0001-")) return "No activity yet";
	const timestamp = new Date(value).getTime();
	if (!Number.isFinite(timestamp)) return value;
	const seconds = Math.round((timestamp - Date.now()) / 1000);
	const formatter = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
	const ranges: Array<[Intl.RelativeTimeFormatUnit, number]> = [
		["year", 31_536_000],
		["month", 2_592_000],
		["day", 86_400],
		["hour", 3_600],
		["minute", 60],
	];
	for (const [unit, divisor] of ranges) {
		if (Math.abs(seconds) >= divisor)
			return formatter.format(Math.round(seconds / divisor), unit);
	}
	return formatter.format(seconds, "second");
}

export function languageFor(path: string) {
	const extension = path.split(".").pop()?.toLowerCase() ?? "";
	const names: Record<string, string> = {
		go: "Go",
		ts: "TypeScript",
		tsx: "TypeScript JSX",
		js: "JavaScript",
		jsx: "JavaScript JSX",
		py: "Python",
		rs: "Rust",
		java: "Java",
		kt: "Kotlin",
		rb: "Ruby",
		php: "PHP",
		css: "CSS",
		scss: "SCSS",
		html: "HTML",
		json: "JSON",
		yaml: "YAML",
		yml: "YAML",
		md: "Markdown",
		sh: "Shell",
		sql: "SQL",
		toml: "TOML",
		xml: "XML",
		c: "C",
		h: "C Header",
		cpp: "C++",
		hpp: "C++ Header",
		swift: "Swift",
		dockerfile: "Dockerfile",
	};
	return (
		names[extension] ?? (path.endsWith("Dockerfile") ? "Dockerfile" : "Text")
	);
}

export function avatarColor(value: string) {
	let hash = 0;
	for (const character of value)
		hash = character.charCodeAt(0) + ((hash << 5) - hash);
	return `hsl(${Math.abs(hash) % 360} 58% 42%)`;
}
