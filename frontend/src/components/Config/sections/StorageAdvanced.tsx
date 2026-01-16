import { Input } from '../../primitives/Input';
import { Toggle } from '../../primitives/Toggle';
import { Select } from '../../primitives/Select';
import type { StorageSettings } from '../../../types/api';

interface StorageAdvancedProps {
  data: StorageSettings;
  onChange: (data: Partial<StorageSettings>) => void;
}

export function StorageAdvanced({ data, onChange }: StorageAdvancedProps) {
  const sslModeOptions = [
    { value: 'disable', label: 'Disabled' },
    { value: 'require', label: 'Require' },
    { value: 'verify-ca', label: 'Verify CA' },
    { value: 'verify-full', label: 'Verify Full' },
  ];

  const updateMinio = (updates: Partial<typeof data.minio>) => {
    onChange({ minio: { ...data.minio, ...updates } });
  };

  const updatePostgres = (updates: Partial<typeof data.postgres>) => {
    onChange({ postgres: { ...data.postgres, ...updates } });
  };

  return (
    <div className="space-y-6">
      {/* MinIO Section */}
      <div>
        <p className="text-xs uppercase tracking-label text-text-secondary mb-3">
          MinIO Object Storage
        </p>
        <div className="space-y-4">
          <Input
            label="Endpoint"
            value={data.minio.endpoint}
            onChange={(e) => updateMinio({ endpoint: e.target.value })}
            helper="e.g., localhost:9000"
          />
          <Input
            label="Bucket"
            value={data.minio.bucket}
            onChange={(e) => updateMinio({ bucket: e.target.value })}
            helper="Bucket name for recordings"
          />
          <div className="grid grid-cols-2 gap-4">
            <Input
              label="Access Key"
              value={data.minio.accessKeyId}
              onChange={(e) => updateMinio({ accessKeyId: e.target.value })}
            />
            <Input
              label="Secret Key"
              type="password"
              value={data.minio.secretAccessKey}
              onChange={(e) => updateMinio({ secretAccessKey: e.target.value })}
            />
          </div>
          <Toggle
            checked={data.minio.useSSL}
            onChange={(useSSL) => updateMinio({ useSSL })}
            label="Use SSL/TLS"
            description="Enable for secure connections"
          />
        </div>
      </div>

      {/* PostgreSQL Section */}
      <div>
        <p className="text-xs uppercase tracking-label text-text-secondary mb-3">
          PostgreSQL Database
        </p>
        <div className="space-y-4">
          <div className="grid grid-cols-3 gap-4">
            <div className="col-span-2">
              <Input
                label="Host"
                value={data.postgres.host}
                onChange={(e) => updatePostgres({ host: e.target.value })}
                helper="e.g., localhost"
              />
            </div>
            <Input
              label="Port"
              type="number"
              value={data.postgres.port}
              onChange={(e) => updatePostgres({ port: parseInt(e.target.value, 10) || 5432 })}
            />
          </div>
          <Input
            label="Database"
            value={data.postgres.database}
            onChange={(e) => updatePostgres({ database: e.target.value })}
          />
          <div className="grid grid-cols-2 gap-4">
            <Input
              label="Username"
              value={data.postgres.username}
              onChange={(e) => updatePostgres({ username: e.target.value })}
            />
            <Input
              label="Password"
              type="password"
              value={data.postgres.password}
              onChange={(e) => updatePostgres({ password: e.target.value })}
            />
          </div>
          <Select
            label="SSL Mode"
            options={sslModeOptions}
            value={data.postgres.sslMode}
            onChange={(e) => updatePostgres({ sslMode: e.target.value })}
          />
        </div>
      </div>
    </div>
  );
}

StorageAdvanced.displayName = 'StorageAdvanced';
