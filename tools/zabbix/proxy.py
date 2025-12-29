"""
Proxy Management Tools for Zabbix

Example:
    from lib.zabbix_py.proxy import proxy_get, proxy_create

    # Get all proxies
    proxies = proxy_get()

    # Create a new active proxy
    result = proxy_create({
        'host': 'proxy-01',
        'status': 5,
        'description': 'Main datacenter proxy'
    })
"""

from typing import TypedDict, Optional, List
from .types import SearchCriteria, FilterCriteria
from .utils import zabbix_request


class ProxyGetParams(TypedDict, total=False):
    """Parameters for proxy.get API method"""
    proxyids: List[str]
    output: str
    search: SearchCriteria
    filter: FilterCriteria
    limit: int


class ProxyCreateParams(TypedDict, total=False):
    """
    Parameters for proxy.create API method

    status: Proxy status (5=active proxy, 6=passive proxy)
    tls_connect: TLS connection settings (1=no encryption, 2=PSK, 4=certificate)
    tls_accept: TLS accept settings (1=no encryption, 2=PSK, 4=certificate)
    """
    host: str
    status: int
    description: str
    tls_connect: int
    tls_accept: int


class ProxyUpdateParams(TypedDict, total=False):
    """Parameters for proxy.update API method"""
    proxyid: str
    host: str
    status: int
    description: str
    tls_connect: int
    tls_accept: int


def proxy_get(params: Optional[ProxyGetParams] = None) -> str:
    """
    Get proxies from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of proxies
    """
    if params is None:
        params = {}
    return zabbix_request('proxy.get', params)


def proxy_create(params: ProxyCreateParams) -> str:
    """
    Create a new proxy in Zabbix

    Args:
        params: Proxy creation parameters

    Returns:
        JSON string containing creation result
    """
    return zabbix_request('proxy.create', params)


def proxy_update(params: ProxyUpdateParams) -> str:
    """
    Update an existing proxy in Zabbix

    Args:
        params: Proxy update parameters

    Returns:
        JSON string containing update result
    """
    return zabbix_request('proxy.update', params)


def proxy_delete(proxyids: List[str]) -> str:
    """
    Delete proxies from Zabbix

    Args:
        proxyids: List of proxy IDs to delete

    Returns:
        JSON string containing deletion result
    """
    return zabbix_request('proxy.delete', {'proxyids': proxyids})
