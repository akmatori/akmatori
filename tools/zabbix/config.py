"""
Zabbix API Configuration

This module manages configuration for direct Zabbix API connections.
Configuration can be set via:
1. Environment variables (.env file)
2. Direct configuration via set_config()

Example using environment variables:
    # Create .env file
    ZABBIX_URL=https://zabbix.example.com
    ZABBIX_TOKEN=your-api-token

Example direct configuration:
    from lib.zabbix_py.config import set_config

    set_config({
        'zabbix_url': 'https://zabbix.example.com',
        'zabbix_token': 'your-api-token'
    })
"""

import os
from typing import Optional, Dict, Any
from copy import deepcopy
from pathlib import Path


def _load_config_env():
    """Load environment variables from config.env file if it exists."""
    # Look for config.env in the same directory as this file
    config_file = Path(__file__).parent / "config.env"
    if not config_file.exists():
        return

    with config_file.open("r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            if "=" not in line:
                continue
            key, value = line.split("=", 1)
            # Only set if not already in environment (don't override existing vars)
            os.environ.setdefault(key, value)


# Auto-load config.env when module is imported
_load_config_env()


class ZabbixConfig:
    """Zabbix API configuration"""

    def __init__(self):
        self.zabbix_url: str = os.getenv('ZABBIX_URL', '')
        self.zabbix_token: Optional[str] = os.getenv('ZABBIX_TOKEN')
        self.zabbix_user: Optional[str] = os.getenv('ZABBIX_USER')
        self.zabbix_password: Optional[str] = os.getenv('ZABBIX_PASSWORD')
        self.timeout: int = int(os.getenv('REQUEST_TIMEOUT', '30'))
        self.debug: bool = os.getenv('DEBUG', '').lower() == 'true'
        self.verify_ssl: bool = os.getenv('VERIFY_SSL', 'true').lower() == 'true'


# Global configuration instance
_config = ZabbixConfig()

# Internal auth state for user/password authentication
_auth_token: Optional[str] = None


def get_config() -> Dict[str, Any]:
    """
    Get current configuration

    Returns:
        Dictionary containing current configuration
    """
    return {
        'zabbix_url': _config.zabbix_url,
        'zabbix_token': _config.zabbix_token,
        'zabbix_user': _config.zabbix_user,
        'zabbix_password': _config.zabbix_password,
        'timeout': _config.timeout,
        'debug': _config.debug,
        'verify_ssl': _config.verify_ssl
    }


def set_config(new_config: Dict[str, Any]) -> None:
    """
    Set configuration (merges with existing config)

    Args:
        new_config: Dictionary with configuration values to update

    Example:
        set_config({
            'zabbix_url': 'https://zabbix.example.com',
            'zabbix_token': 'your-api-token'
        })
    """
    global _auth_token

    if 'zabbix_url' in new_config:
        _config.zabbix_url = new_config['zabbix_url']
    if 'zabbix_token' in new_config:
        _config.zabbix_token = new_config['zabbix_token']
    if 'zabbix_user' in new_config:
        _config.zabbix_user = new_config['zabbix_user']
    if 'zabbix_password' in new_config:
        _config.zabbix_password = new_config['zabbix_password']
    if 'timeout' in new_config:
        _config.timeout = new_config['timeout']
    if 'debug' in new_config:
        _config.debug = new_config['debug']
    if 'verify_ssl' in new_config:
        _config.verify_ssl = new_config['verify_ssl']

    # Clear auth token when credentials change
    if any(k in new_config for k in ['zabbix_user', 'zabbix_password', 'zabbix_token']):
        _auth_token = None

    if _config.debug:
        print(f'[Zabbix API] Configuration updated: '
              f'zabbix_url={_config.zabbix_url}, '
              f'has_token={bool(_config.zabbix_token)}, '
              f'has_user={bool(_config.zabbix_user)}, '
              f'timeout={_config.timeout}')


def reset_config() -> None:
    """Reset configuration to defaults (from environment variables)"""
    global _config, _auth_token
    _config = ZabbixConfig()
    _auth_token = None


def get_zabbix_api_url() -> str:
    """
    Get Zabbix API URL

    Returns:
        Full URL to Zabbix API endpoint

    Raises:
        ValueError: If Zabbix URL is not configured
    """
    if not _config.zabbix_url:
        raise ValueError(
            'Zabbix URL not configured. Set ZABBIX_URL environment variable '
            'or call set_config()'
        )

    # Ensure URL ends with /api_jsonrpc.php
    base_url = _config.zabbix_url.rstrip('/')
    if base_url.endswith('/api_jsonrpc.php'):
        return base_url
    return f'{base_url}/api_jsonrpc.php'


def authenticate() -> str:
    """
    Authenticate with username/password and get auth token

    Returns:
        Authentication token

    Raises:
        ValueError: If username/password not configured
        Exception: If authentication fails
    """
    global _auth_token

    if _auth_token:
        return _auth_token

    if not _config.zabbix_user or not _config.zabbix_password:
        raise ValueError('Zabbix username and password not configured')

    import requests

    url = get_zabbix_api_url()
    body = {
        'jsonrpc': '2.0',
        'method': 'user.login',
        'params': {
            'username': _config.zabbix_user,
            'password': _config.zabbix_password
        },
        'id': 1
    }

    if _config.debug:
        print(f'[Zabbix API] Authenticating user: {_config.zabbix_user}')

    response = requests.post(
        url,
        json=body,
        headers={'Content-Type': 'application/json-rpc'},
        verify=_config.verify_ssl,
        timeout=_config.timeout
    )

    if not response.ok:
        raise Exception(f'Authentication failed: {response.status_code} {response.text}')

    result = response.json()

    if 'error' in result:
        error = result['error']
        raise Exception(
            f"Authentication failed: {error.get('message', 'Unknown error')} "
            f"(code: {error.get('code', 'unknown')})"
        )

    _auth_token = result['result']

    if _config.debug:
        print('[Zabbix API] Authentication successful')

    return _auth_token


def get_auth() -> Optional[str]:
    """
    Get authentication parameter for request body

    Returns:
        Authentication token or None

    Raises:
        ValueError: If no authentication method is configured
    """
    if _config.zabbix_token:
        return _config.zabbix_token

    # For user/password, we need to get the auth token
    if _config.zabbix_user and _config.zabbix_password:
        return authenticate()

    raise ValueError(
        'No authentication method configured. '
        'Set ZABBIX_TOKEN or ZABBIX_USER/ZABBIX_PASSWORD'
    )


def debug_log(message: str, *args: Any) -> None:
    """
    Debug log helper

    Args:
        message: Log message
        *args: Additional arguments to print
    """
    if _config.debug:
        if args:
            print(f'[Zabbix API] {message}', *args)
        else:
            print(f'[Zabbix API] {message}')


def get_timeout() -> int:
    """Get configured timeout in seconds"""
    return _config.timeout


def get_verify_ssl() -> bool:
    """Get SSL verification setting"""
    return _config.verify_ssl
