"""
Graph Management Tools for Zabbix

Example:
    from lib.zabbix_py.graph import graph_get

    # Get all graphs for a host
    graphs = graph_get({'hostids': ['10001']})
"""

from typing import TypedDict, Optional, List
from .types import OutputFormat, SearchCriteria, FilterCriteria
from .utils import zabbix_request


class GraphGetParams(TypedDict, total=False):
    """Parameters for graph.get API method"""
    graphids: List[str]
    hostids: List[str]
    templateids: List[str]
    output: OutputFormat
    search: SearchCriteria
    filter: FilterCriteria


def graph_get(params: Optional[GraphGetParams] = None) -> str:
    """
    Get graphs from Zabbix with optional filtering

    Args:
        params: Optional filtering parameters

    Returns:
        JSON string containing list of graphs
    """
    if params is None:
        params = {}
    return zabbix_request('graph.get', params)
