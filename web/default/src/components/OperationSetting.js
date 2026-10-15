import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Divider,
  Dropdown,
  Form,
  Grid,
  Header,
  Icon,
  Input,
  Label,
  Table,
} from 'semantic-ui-react';
import {
  API,
  showError,
  showSuccess,
  timestamp2string,
  verifyJSON,
} from '../helpers';


// ---- 模型别名表格编辑：解析 / 序列化 ----
// 兼容三种形态：字符串（单后端）、数组（多后端默认轮询）、对象（多后端+策略）
const ALIAS_STRATEGIES = ['round_robin', 'random', 'ip_hash', 'cookie_hash', 'primary_backup'];

function parseAliasRows(value) {
  if (!value || value.trim() === '') return [];
  try {
    const obj = JSON.parse(value);
    return Object.entries(obj).map(([alias, v]) => {
      if (Array.isArray(v)) {
        return { alias, models: v, strategy: 'round_robin' };
      }
      if (v && typeof v === 'object') {
        return {
          alias,
          models: Array.isArray(v.models) ? v.models : [],
          strategy: v.strategy || 'round_robin',
        };
      }
      return { alias, models: [String(v)], strategy: 'primary_backup' };
    });
  } catch (e) {
    return [];
  }
}

function serializeAliasRows(rows) {
  const obj = {};
  rows.forEach((row) => {
    const alias = (row.alias || '').trim();
    const models = (row.models || []).filter((mm) => mm && mm.trim() !== '');
    if (alias === '' || models.length === 0) return;
    if (models.length === 1) {
      obj[alias] = models[0];
    } else if (row.strategy === 'round_robin') {
      obj[alias] = models;
    } else {
      obj[alias] = { models, strategy: row.strategy };
    }
  });
  return JSON.stringify(obj, null, 2);
}

const OperationSetting = () => {
  const { t } = useTranslation();
  let now = new Date();
  let [inputs, setInputs] = useState({
    ModelAlias: '',
    ModelContextTokens: '',
    ChatLink: '',
    QuotaPerUnit: 0,
    AutomaticDisableChannelEnabled: '',
    AutomaticEnableChannelEnabled: '',
    ChannelDisableThreshold: 0,
    LogConsumeEnabled: '',
    DisplayInCurrencyEnabled: '',
    DisplayTokenStatEnabled: '',
    ApproximateTokenEnabled: '',
    RetryTimes: 0,
  });
  const [originInputs, setOriginInputs] = useState({});
  const [modelOptions, setModelOptions] = useState([]);
  const [aliasRows, setAliasRows] = useState([]);
  const [aliasSavedValue, setAliasSavedValue] = useState(null);
  let [loading, setLoading] = useState(false);
  let [historyTimestamp, setHistoryTimestamp] = useState(
    timestamp2string(now.getTime() / 1000 - 30 * 24 * 3600)
  ); // a month ago

  const getOptions = async () => {
    const res = await API.get('/api/option/');
    const { success, message, data } = res.data;
    if (success) {
      let newInputs = {};
      data.forEach((item) => {
        if (item.key === 'ModelAlias' || item.key === 'ModelContextTokens') {
          item.value = JSON.stringify(JSON.parse(item.value), null, 2);
        }
        if (item.value === '{}') {
          item.value = '';
        }
        newInputs[item.key] = item.value;
      });
      setInputs(newInputs);
      setOriginInputs(newInputs);
    } else {
      showError(message);
    }
  };

  useEffect(() => {
    getOptions().then();
    API.get('/api/models/configured')
      .then((res) => {
        const list = res.data && res.data.success ? res.data.data : [];
        setModelOptions(list.map((m) => ({ key: m, text: m, value: m })));
      })
      .catch(() => {});
  }, []);

  // ModelAlias 配置被外部更新（如加载/保存成功）时，同步到表格行
  useEffect(() => {
    if (inputs.ModelAlias !== aliasSavedValue) {
      setAliasRows(parseAliasRows(inputs.ModelAlias));
      setAliasSavedValue(inputs.ModelAlias);
    }
  }, [inputs.ModelAlias]);

  const saveAliasRows = async () => {
    const json = serializeAliasRows(aliasRows);
    setInputs((prev) => ({ ...prev, ModelAlias: json }));
    await updateOption('ModelAlias', json === '{}' ? '' : json);
    setOriginInputs((prev) => ({ ...prev, ModelAlias: json }));
    setAliasRows(parseAliasRows(json));
    setAliasSavedValue(json);
  };

  const updateOption = async (key, value) => {
    setLoading(true);
    if (key.endsWith('Enabled')) {
      value = inputs[key] === 'true' ? 'false' : 'true';
    }
    const res = await API.put('/api/option/', {
      key,
      value,
    });
    const { success, message } = res.data;
    if (success) {
      setInputs((inputs) => ({ ...inputs, [key]: value }));
    } else {
      showError(message);
    }
    setLoading(false);
  };

  const handleInputChange = async (e, { name, value }) => {
    if (name.endsWith('Enabled')) {
      await updateOption(name, value);
    } else {
      setInputs((inputs) => ({ ...inputs, [name]: value }));
    }
  };

  const submitConfig = async (group) => {
    switch (group) {
      case 'monitor':
        if (
          originInputs['ChannelDisableThreshold'] !==
          inputs.ChannelDisableThreshold
        ) {
          await updateOption(
            'ChannelDisableThreshold',
            inputs.ChannelDisableThreshold
          );
        }
        break;
      case 'ratio':
        if (originInputs['ModelRatio'] !== inputs.ModelRatio) {
          if (!verifyJSON(inputs.ModelRatio)) {
            showError('模型倍率不是合法的 JSON 字符串');
            return;
          }
          await updateOption('ModelRatio', inputs.ModelRatio);
        }
        if (originInputs['GroupRatio'] !== inputs.GroupRatio) {
          if (!verifyJSON(inputs.GroupRatio)) {
            showError('分组倍率不是合法的 JSON 字符串');
            return;
          }
          await updateOption('GroupRatio', inputs.GroupRatio);
        }
        if (originInputs['CompletionRatio'] !== inputs.CompletionRatio) {
          if (!verifyJSON(inputs.CompletionRatio)) {
            showError('补全倍率不是合法的 JSON 字符串');
            return;
          }
          await updateOption('CompletionRatio', inputs.CompletionRatio);
        }
        break;
      case 'quota':
        if (originInputs['QuotaForNewUser'] !== inputs.QuotaForNewUser) {
          await updateOption('QuotaForNewUser', inputs.QuotaForNewUser);
        }
        if (originInputs['QuotaForInvitee'] !== inputs.QuotaForInvitee) {
          await updateOption('QuotaForInvitee', inputs.QuotaForInvitee);
        }
        if (originInputs['QuotaForInviter'] !== inputs.QuotaForInviter) {
          await updateOption('QuotaForInviter', inputs.QuotaForInviter);
        }
        if (originInputs['PreConsumedQuota'] !== inputs.PreConsumedQuota) {
          await updateOption('PreConsumedQuota', inputs.PreConsumedQuota);
        }
        break;
      case 'general':
        if (originInputs['ChatLink'] !== inputs.ChatLink) {
          await updateOption('ChatLink', inputs.ChatLink);
        }
        if (originInputs['QuotaPerUnit'] !== inputs.QuotaPerUnit) {
          await updateOption('QuotaPerUnit', inputs.QuotaPerUnit);
        }
        if (originInputs['RetryTimes'] !== inputs.RetryTimes) {
          await updateOption('RetryTimes', inputs.RetryTimes);
        }
        break;
    }
  };

  const deleteHistoryLogs = async () => {
    console.log(inputs);
    const res = await API.delete(
      `/api/log/?target_timestamp=${Date.parse(historyTimestamp) / 1000}`
    );
    const { success, message, data } = res.data;
    if (success) {
      showSuccess(`${data} 条日志已清理！`);
      return;
    }
    showError('日志清理失败：' + message);
  };

  return (
    <Grid columns={1}>
      <Grid.Column>
        <Form loading={loading}>
          <Header as='h3'>{t('setting.operation.alias.title')}</Header>
          <Label basic style={{ marginBottom: '1em' }}>
            {t('setting.operation.alias.hint')}
          </Label>
          <Table basic='very' size='small'>
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell width={4}>{t('setting.operation.alias.col_alias')}</Table.HeaderCell>
                <Table.HeaderCell width={7}>{t('setting.operation.alias.col_models')}</Table.HeaderCell>
                <Table.HeaderCell width={3}>{t('setting.operation.alias.col_strategy')}</Table.HeaderCell>
                <Table.HeaderCell width={2}></Table.HeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {aliasRows.map((row, idx) => (
                <Table.Row key={idx}>
                  <Table.Cell>
                    <Input
                      fluid
                      value={row.alias}
                      placeholder='my-model'
                      onChange={(e, { value }) => {
                        const rows = [...aliasRows];
                        rows[idx] = { ...row, alias: value };
                        setAliasRows(rows);
                      }}
                    />
                  </Table.Cell>
                  <Table.Cell>
                    <Dropdown
                      fluid
                      multiple
                      search
                      selection
                      allowAdditions
                      additionLabel='新增模型: '
                      options={modelOptions}
                      value={row.models}
                      placeholder={t('setting.operation.alias.models_placeholder')}
                      onChange={(e, { value }) => {
                        const rows = [...aliasRows];
                        rows[idx] = { ...row, models: value };
                        setAliasRows(rows);
                      }}
                    />
                  </Table.Cell>
                  <Table.Cell>
                    {row.models.length > 1 ? (
                      <Dropdown
                        fluid
                        selection
                        options={ALIAS_STRATEGIES.map((st) => ({ key: st, text: st, value: st }))}
                        value={row.strategy}
                        onChange={(e, { value }) => {
                          const rows = [...aliasRows];
                          rows[idx] = { ...row, strategy: value };
                          setAliasRows(rows);
                        }}
                      />
                    ) : (
                      <span style={{ color: '#999' }}>—</span>
                    )}
                  </Table.Cell>
                  <Table.Cell>
                    <Button
                      size='tiny'
                      negative
                      icon
                      onClick={() => {
                        setAliasRows(aliasRows.filter((_, i) => i !== idx));
                      }}
                    >
                      <Icon name='trash' />
                    </Button>
                  </Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
          <Button
            size='tiny'
            onClick={() => {
              setAliasRows([...aliasRows, { alias: '', models: [], strategy: 'round_robin' }]);
            }}
          >
            <Icon name='plus' /> {t('setting.operation.alias.add_row')}
          </Button>{' '}
          <Form.Button
            primary
            onClick={() => {
              saveAliasRows().then();
            }}
          >
            {t('setting.operation.alias.buttons.save')}
          </Form.Button>
          <Divider />
          <Header as='h3'>{t('setting.operation.log.title')}</Header>
          <Form.Group inline>
            <Form.Checkbox
              checked={inputs.LogConsumeEnabled === 'true'}
              label={t('setting.operation.log.enable_consume')}
              name='LogConsumeEnabled'
              onChange={handleInputChange}
            />
          </Form.Group>
          <Form.Group widths={4}>
            <Form.Input
              label={t('setting.operation.log.target_time')}
              value={historyTimestamp}
              type='datetime-local'
              name='history_timestamp'
              onChange={(e, { name, value }) => {
                setHistoryTimestamp(value);
              }}
            />
          </Form.Group>
          <Form.Button
            onClick={() => {
              deleteHistoryLogs().then();
            }}
          >
            {t('setting.operation.log.buttons.clean')}
          </Form.Button>

          <Divider />
          <Header as='h3'>{t('setting.operation.monitor.title')}</Header>
          <Form.Group>
            <Form.Input
              label={t('setting.operation.monitor.max_response_time')}
              name='ChannelDisableThreshold'
              onChange={handleInputChange}
              autoComplete='new-password'
              value={inputs.ChannelDisableThreshold}
              type='number'
              min='0'
              placeholder={t(
                'setting.operation.monitor.max_response_time_placeholder'
              )}
            />

          </Form.Group>
          <Form.Group inline>
            <Form.Checkbox
              checked={inputs.AutomaticDisableChannelEnabled === 'true'}
              label={t('setting.operation.monitor.auto_disable')}
              name='AutomaticDisableChannelEnabled'
              onChange={handleInputChange}
            />
            <Form.Checkbox
              checked={inputs.AutomaticEnableChannelEnabled === 'true'}
              label={t('setting.operation.monitor.auto_enable')}
              name='AutomaticEnableChannelEnabled'
              onChange={handleInputChange}
            />
          </Form.Group>
          <Form.Button
            onClick={() => {
              submitConfig('monitor').then();
            }}
          >
            {t('setting.operation.monitor.buttons.save')}
          </Form.Button>

          <Divider />
          <Header as='h3'>{t('setting.operation.general.title')}</Header>
          <Form.Group widths={4}>
            <Form.Input
              label={t('setting.operation.general.chat_link')}
              name='ChatLink'
              onChange={handleInputChange}
              autoComplete='new-password'
              value={inputs.ChatLink}
              type='link'
              placeholder={t('setting.operation.general.chat_link_placeholder')}
            />
            <Form.Input
              label={t('setting.operation.general.quota_per_unit')}
              name='QuotaPerUnit'
              onChange={handleInputChange}
              autoComplete='new-password'
              value={inputs.QuotaPerUnit}
              type='number'
              step='0.01'
              placeholder={t(
                'setting.operation.general.quota_per_unit_placeholder'
              )}
            />
            <Form.Input
              label={t('setting.operation.general.retry_times')}
              name='RetryTimes'
              type={'number'}
              step='1'
              min='0'
              onChange={handleInputChange}
              autoComplete='new-password'
              value={inputs.RetryTimes}
              placeholder={t(
                'setting.operation.general.retry_times_placeholder'
              )}
            />
          </Form.Group>
          <Form.Group inline>
            <Form.Checkbox
              checked={inputs.DisplayInCurrencyEnabled === 'true'}
              label={t('setting.operation.general.display_in_currency')}
              name='DisplayInCurrencyEnabled'
              onChange={handleInputChange}
            />
            <Form.Checkbox
              checked={inputs.DisplayTokenStatEnabled === 'true'}
              label={t('setting.operation.general.display_token_stat')}
              name='DisplayTokenStatEnabled'
              onChange={handleInputChange}
            />
            <Form.Checkbox
              checked={inputs.ApproximateTokenEnabled === 'true'}
              label={t('setting.operation.general.approximate_token')}
              name='ApproximateTokenEnabled'
              onChange={handleInputChange}
            />
          </Form.Group>
          <Form.Button
            onClick={() => {
              submitConfig('general').then();
            }}
          >
            {t('setting.operation.general.buttons.save')}
          </Form.Button>
        </Form>
      </Grid.Column>
    </Grid>
  );
};

export default OperationSetting;
