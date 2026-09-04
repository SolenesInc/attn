// Adapted Gitleaks token rules; source, changes and MIT notice are in notices/.
export const credentialPatterns: RegExp[] = [
  /\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16,})\b/g, // aws-access-token
  /\b(AIza[\w-]{35,})/g, // gcp-api-key
  /(?:ghu|ghs)_[0-9a-zA-Z]{36,}/g, // github-app-token
  /github_pat_\w{82,}/g, // github-fine-grained-pat
  /gho_[0-9a-zA-Z]{36,}/g, // github-oauth
  /ghp_[0-9a-zA-Z]{36,}/g, // github-pat
  /ghr_[0-9a-zA-Z]{36,}/g, // github-refresh-token
  /glpat-[\w-]{20,}/g, // gitlab-pat
  /gldt-[0-9a-zA-Z_-]{20,}/g, // gitlab-deploy-token
  /glrt-[0-9a-zA-Z_-]{20,}/g, // gitlab-runner-authentication-token
  /\b(npm_[a-z0-9]{36,})/gi, // npm-access-token
  /\b((?:sk|rk)_(?:test|live|prod)_[a-zA-Z0-9]{10,})/g, // stripe-access-token
  /SK[0-9a-fA-F]{32,}/g, // twilio-api-key
  /xapp-\d-[A-Z0-9]+-\d+-[a-z0-9]+/gi, // slack-app-token
  /xoxb-[0-9]{10,}-[0-9]{10,}[a-zA-Z0-9-]*/g, // slack-bot-token
  /xox[pe](?:-[0-9]{10,}){3,}-[a-zA-Z0-9-]{28,}/g, // slack-user-token
  /xox[os]-\d+-\d+-\d+-[a-fA-F\d]+/g, // slack-legacy-token
  /xox[ar]-(?:\d-)?[0-9a-zA-Z]{8,}/g, // slack-legacy-workspace-token
  /xoxe.xox[bp]-\d-[A-Z0-9]{163,}/gi, // slack-config-access-token
  /xoxe-\d-[A-Z0-9]{146,}/gi, // slack-config-refresh-token
  /\bsk-[\w-]{20,}/g,
  /\bSG\.[\w.=-]{66,}/g,
  /\b(?:key|pubkey)-[a-f\d]{32,}/gi,
  /\bAC[a-f\d]{32,}/gi,
  /\b(?:gsk_|xai-)[\w-]{20,}/g,
  /\beyJ[\w-]+\.[\w-]+\.[\w-]+/g,
  /\bBearer\s+[^\s\x22\x27,;]+/gi,
  /\b[\w-]*(?:api[-_]?key|token|secret|auth(?:orization)?|credential|identity)[\w-]*:\s*(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^\s,;]+)/gi,
  /\b\w*(?:TOKEN|SECRET|PASSWORD|API_KEY|PRIVATE_KEY)\w*\s*=\s*(?:\x22[^\x22\n]*\x22|\x27[^\x27\n]*\x27|[^\s,;]+)/gi,
];
