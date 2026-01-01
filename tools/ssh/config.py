"""
SSH Tool Configuration

Configuration is loaded from environment variables, typically set via .env.ssh file
when the tool is assigned to a skill.

Environment Variables:
    SSH_PRIVATE_KEY: SSH private key content (PEM format)
    SSH_SERVERS: Comma-separated list of server hostnames/IPs
    SSH_USERNAME: SSH username
    SSH_PORT: SSH port (default: 22)
    SSH_COMMAND_TIMEOUT: Command execution timeout in seconds (default: 30)
    SSH_CONNECTION_TIMEOUT: Connection timeout in seconds (default: 10)
    SSH_KNOWN_HOSTS_POLICY: Host key policy (strict/auto_add/ignore, default: auto_add)
    SSH_DEBUG: Enable debug logging (default: false)
"""

import base64
import os
from typing import Optional, Dict, Any, List
from pathlib import Path


def _decode_env_value(value: str) -> str:
    """
    Decode environment variable value.
    Values prefixed with 'base64:' are base64-decoded to support multi-line secrets.
    """
    if value.startswith('base64:'):
        try:
            return base64.b64decode(value[7:]).decode('utf-8')
        except Exception:
            return value
    return value


def _load_env_file(file_path: Path) -> None:
    """Load environment variables from a .env file."""
    if not file_path.exists():
        return

    with file_path.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" not in line:
                continue
            key, value = line.split("=", 1)
            os.environ.setdefault(key, _decode_env_value(value))


def _load_config_env():
    """
    Load environment variables from config files.

    Search order (first found wins for each variable):
    1. Current working directory .env.ssh (incident directory)
    2. Tool's own config.env (tool directory)
    """
    # Try loading from incident directory first (.env.ssh)
    cwd_env = Path.cwd() / ".env.ssh"
    _load_env_file(cwd_env)

    # Then try tool's own config.env
    tool_env = Path(__file__).parent / "config.env"
    _load_env_file(tool_env)


# Auto-load config.env when module is imported
_load_config_env()


class SSHConfig:
    """SSH Tool configuration"""

    def __init__(self):
        self.private_key: str = os.getenv('SSH_PRIVATE_KEY', '')
        self.servers: List[str] = self._parse_servers(os.getenv('SSH_SERVERS', ''))
        self.username: str = os.getenv('SSH_USERNAME', '')
        self.port: int = int(os.getenv('SSH_PORT', '22'))
        self.command_timeout: int = int(os.getenv('SSH_COMMAND_TIMEOUT', '30'))
        self.connection_timeout: int = int(os.getenv('SSH_CONNECTION_TIMEOUT', '10'))
        self.known_hosts_policy: str = os.getenv('SSH_KNOWN_HOSTS_POLICY', 'auto_add')
        self.debug: bool = os.getenv('SSH_DEBUG', '').lower() == 'true'

    @staticmethod
    def _parse_servers(servers_str: str) -> List[str]:
        """Parse comma-separated server list"""
        if not servers_str:
            return []
        return [s.strip() for s in servers_str.split(',') if s.strip()]


# Global configuration instance
_config = SSHConfig()


def get_config() -> Dict[str, Any]:
    """Get current configuration (excluding sensitive data)"""
    return {
        'servers': _config.servers,
        'username': _config.username,
        'port': _config.port,
        'command_timeout': _config.command_timeout,
        'connection_timeout': _config.connection_timeout,
        'known_hosts_policy': _config.known_hosts_policy,
        'debug': _config.debug,
        'has_private_key': bool(_config.private_key),
    }


def set_config(new_config: Dict[str, Any]) -> None:
    """Set configuration (merges with existing config)"""
    # Helper to get value with or without ssh_ prefix
    def get_val(key: str) -> Any:
        return new_config.get(f'ssh_{key}') or new_config.get(key)

    if get_val('private_key'):
        _config.private_key = get_val('private_key')
    if get_val('servers'):
        val = get_val('servers')
        # Handle both list and comma-separated string
        if isinstance(val, list):
            _config.servers = val
        elif isinstance(val, str):
            _config.servers = [s.strip() for s in val.split(',') if s.strip()]
    if get_val('username'):
        _config.username = get_val('username')
    if get_val('port'):
        _config.port = int(get_val('port'))
    if get_val('command_timeout'):
        _config.command_timeout = int(get_val('command_timeout'))
    if get_val('connection_timeout'):
        _config.connection_timeout = int(get_val('connection_timeout'))
    if get_val('known_hosts_policy'):
        _config.known_hosts_policy = get_val('known_hosts_policy')
    if 'debug' in new_config:
        _config.debug = new_config['debug']


def reset_config() -> None:
    """Reset configuration to defaults (from environment variables)"""
    global _config
    _config = SSHConfig()


def get_private_key() -> str:
    """Get private key content"""
    return _config.private_key


def get_servers() -> List[str]:
    """Get list of configured servers"""
    return _config.servers


def get_username() -> str:
    """Get SSH username"""
    return _config.username


def get_port() -> int:
    """Get SSH port"""
    return _config.port


def get_command_timeout() -> int:
    """Get command execution timeout in seconds"""
    return _config.command_timeout


def get_connection_timeout() -> int:
    """Get connection timeout in seconds"""
    return _config.connection_timeout


def get_known_hosts_policy() -> str:
    """Get known hosts verification policy"""
    return _config.known_hosts_policy


def debug_log(message: str, *args: Any) -> None:
    """Debug log helper"""
    if _config.debug:
        if args:
            print(f'[SSH Tool] {message}', *args)
        else:
            print(f'[SSH Tool] {message}')
