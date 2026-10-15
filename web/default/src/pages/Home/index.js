import React, { useContext, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Header, Label, Table } from 'semantic-ui-react';
import { API } from '../../helpers';
import { UserContext } from '../../context/User';
import Dashboard from '../Dashboard';

const ModelStatusTable = () => {
  const { t } = useTranslation();
  const [models, setModels] = useState([]);
  const [loaded, setLoaded] = useState(false);
  const [userState] = useContext(UserContext);

  const load = async () => {
    if (!userState.user) {
      setLoaded(true);
      return;
    }
    try {
      const res = await API.get('/api/models/status');
      const { success, data } = res.data;
      if (success) {
        setModels(data || []);
      }
    } catch (e) {
      // ignore
    }
    setLoaded(true);
  };

  useEffect(() => {
    load().then();
    const timer = setInterval(() => load().then(), 60000); // 每分钟自动刷新
    return () => clearInterval(timer);
  }, [userState.user]);

  if (!loaded) {
    return null;
  }

  return (
    <Card fluid className='chart-card'>
      <Card.Content>
        <Card.Header>
          <Header as='h3'>{t('home.models_status.title')}</Header>
        </Card.Header>
        {!userState.user ? (
          <Card.Description>{t('home.models_status.login_required')}</Card.Description>
        ) : models.length === 0 ? (
          <Card.Description>{t('home.models_status.empty')}</Card.Description>
        ) : (
          <Table basic='very' celled collapsing size='small'>
            <Table.Header>
              <Table.Row>
                <Table.HeaderCell>{t('home.models_status.model')}</Table.HeaderCell>
                <Table.HeaderCell>{t('home.models_status.status')}</Table.HeaderCell>
                <Table.HeaderCell>{t('home.models_status.response_time')}</Table.HeaderCell>
                <Table.HeaderCell>{t('home.models_status.channels')}</Table.HeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {models.map((m) => (
                <Table.Row key={m.model}>
                  <Table.Cell>{m.model}</Table.Cell>
                  <Table.Cell>
                    {m.available ? (
                      <Label basic color='green'>
                        {t('home.models_status.available')}
                      </Label>
                    ) : (
                      <Label basic color='red'>
                        {t('home.models_status.unavailable')}
                      </Label>
                    )}
                  </Table.Cell>
                  <Table.Cell>
                    {m.response_time > 0 ? `${m.response_time} ms` : '—'}
                  </Table.Cell>
                  <Table.Cell>{m.channels}</Table.Cell>
                </Table.Row>
              ))}
            </Table.Body>
          </Table>
        )}
      </Card.Content>
    </Card>
  );
};

const Home = () => {
  const { t } = useTranslation();

  return (
    <div className='dashboard-container'>
      <ModelStatusTable />
      <Card fluid className='chart-card'>
        <Card.Content>
          <Card.Header>
            <Header as='h3'>{t('home.overview.title')}</Header>
          </Card.Header>
          <Card.Description>
            <Dashboard />
          </Card.Description>
        </Card.Content>
      </Card>
    </div>
  );
};

export default Home;
