import type { MCPAuthConfigInput, MCPAuthType } from '../../types/connectors';

export interface MCPAuthForm {
  type: MCPAuthType;
  token: string;
  headerName: string;
  headerValue: string;
  clientId: string;
  clientSecret: string;
  scope: string;
}

export const EMPTY_AUTH_FORM: MCPAuthForm = {
  type: 'none',
  token: '',
  headerName: 'X-API-Key',
  headerValue: '',
  clientId: '',
  clientSecret: '',
  scope: '',
};

// authInputFromForm builds the auth_config to send. Empty secrets are sent
// as missing, which on update keeps the stored secret.
export function authInputFromForm(f: MCPAuthForm): MCPAuthConfigInput | undefined {
  switch (f.type) {
    case 'bearer':
      return { type: 'bearer', token: f.token || undefined };
    case 'header':
      return { type: 'header', header_name: f.headerName.trim(), value: f.headerValue || undefined };
    case 'oauth': {
      const out: MCPAuthConfigInput = { type: 'oauth' };
      if (f.clientId.trim()) out.client_id = f.clientId.trim();
      if (f.clientSecret) out.client_secret = f.clientSecret;
      if (f.scope.trim()) out.scope = f.scope.trim();
      return out;
    }
    default:
      return undefined;
  }
}
