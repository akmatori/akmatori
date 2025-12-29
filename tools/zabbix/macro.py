"""
User Macro Management Tools for Zabbix

Example:
    from lib.zabbix_py.macro import usermacro_get

    # Get all global macros
    macros = usermacro_get()

    # Get macros for specific host
    host_macros = usermacro_get({'hostids': ['10001']})
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class UsermacroGetParams(TypedDict, total=False):
    """Parameters for usermacro.get API method"""
    globalmacroids: List[str]
    hostids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


def usermacro_get(params: Optional[UsermacroGetParams] = None) -> str:
    """
    Get global macros from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of user macros
    """
    if params is None:
        params = {}
    return zabbix_request('usermacro.get', params)
