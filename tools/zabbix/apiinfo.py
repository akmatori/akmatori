"""
API Information Tools for Zabbix

Example:
    from lib.zabbix_py.apiinfo import apiinfo_version

    # Get Zabbix API version
    version = apiinfo_version()
"""

from .utils import zabbix_request


def apiinfo_version() -> str:
    """
    Get Zabbix API version information

    Returns:
        JSON string containing version information
    """
    return zabbix_request('apiinfo.version', {})
