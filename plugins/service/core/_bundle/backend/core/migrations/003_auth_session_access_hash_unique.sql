CREATE UNIQUE INDEX auth_sessions_access_token_hash_unique
  ON auth_sessions (access_token_hash);
