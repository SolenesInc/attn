CREATE TABLE code_nav_setups (
	host TEXT NOT NULL,
	repository TEXT NOT NULL,
	language TEXT NOT NULL,
	session_id TEXT NOT NULL,
	PRIMARY KEY (host, repository, language)
);
