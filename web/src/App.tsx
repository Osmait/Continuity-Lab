import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AdminPage } from "./AdminPage";
import { AppHeader } from "./components";
import { HomePage } from "./HomePage";
import { RepoPage } from "./RepoPage";

export default function App() {
	const location = useLocation();
	const isAdmin =
		location.pathname === "/admin" || location.pathname.startsWith("/admin/");

	if (isAdmin) {
		return (
			<Routes>
				<Route path="/admin/*" element={<AdminPage />} />
			</Routes>
		);
	}

	return (
		<div className="app-shell">
			<AppHeader />
			<Routes>
				<Route path="/" element={<HomePage />} />
				<Route path="/repos/*" element={<RepoPage />} />
				<Route path="*" element={<Navigate to="/" replace />} />
			</Routes>
			<footer className="app-footer">
				<span>Continuity Git</span>
				<span>Browse code. Inspect history.</span>
				<span>Educational use only</span>
			</footer>
		</div>
	);
}
