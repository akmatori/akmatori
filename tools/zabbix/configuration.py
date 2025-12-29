"""
Configuration Export/Import Tools for Zabbix

Example:
    from lib.zabbix_py.configuration import configuration_export, configuration_import

    # Export configuration
    config = configuration_export({
        'format': 'json',
        'options': {'hosts': ['10001']}
    })

    # Import configuration
    result = configuration_import({
        'format': 'json',
        'source': config_data,
        'rules': {'hosts': {'createMissing': True}}
    })
"""

from typing import TypedDict, Optional
from .types import ExportOptions, ImportRules
from .utils import zabbix_request


class ConfigurationExportParams(TypedDict, total=False):
    """
    Parameters for configuration.export API method

    format: Export format (json, xml)
    """
    format: str
    options: ExportOptions


class ConfigurationImportParams(TypedDict):
    """
    Parameters for configuration.import API method

    format: Import format (json, xml)
    source: Configuration data to import
    rules: Import rules
    """
    format: str
    source: str
    rules: ImportRules


def configuration_export(params: Optional[ConfigurationExportParams] = None) -> str:
    """
    Export configuration from Zabbix

    Args:
        params: Export parameters

    Returns:
        JSON string containing exported configuration
    """
    if params is None:
        params = {}
    return zabbix_request('configuration.export', params)


def configuration_import(params: ConfigurationImportParams) -> str:
    """
    Import configuration to Zabbix

    Args:
        params: Import parameters

    Returns:
        JSON string containing import result
    """
    return zabbix_request('configuration.import', params)
