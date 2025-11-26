import type { StorageSettings } from '../../types/api';

interface StorageSectionProps {
  data: StorageSettings;
  onChange: (data: Partial<StorageSettings>) => void;
}

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function StorageSection({ data, onChange }: StorageSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Storage Settings</h2>

      <div className="space-y-6">
        {/* MinIO Settings */}
        <div>
          <h3 className="text-lg font-medium mb-3 text-white">MinIO Object Storage</h3>
          <div className="space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Endpoint
                </label>
                <input
                  type="text"
                  value={data.minio?.endpoint || ''}
                  onChange={(e) => onChange({ minio: { ...data.minio, endpoint: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="e.g., localhost:9000"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Bucket Name
                </label>
                <input
                  type="text"
                  value={data.minio?.bucket || ''}
                  onChange={(e) => onChange({ minio: { ...data.minio, bucket: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="e.g., recordings"
                />
              </div>
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Access Key ID
                </label>
                <input
                  type="text"
                  value={data.minio?.accessKeyId || ''}
                  onChange={(e) => onChange({ minio: { ...data.minio, accessKeyId: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Access key"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Secret Access Key
                </label>
                <input
                  type="password"
                  value={data.minio?.secretAccessKey || ''}
                  onChange={(e) => onChange({ minio: { ...data.minio, secretAccessKey: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Enter secret key"
                />
                <p className="text-xs text-gray-400 mt-1">
                  {data.minio?.secretAccessKey ? 'Password is set (shown encrypted)' : 'Required for MinIO authentication'}
                </p>
              </div>
            </div>

            <div>
              <label className="flex items-center space-x-3">
                <input
                  type="checkbox"
                  checked={data.minio?.useSSL || false}
                  onChange={(e) => onChange({ minio: { ...data.minio, useSSL: e.target.checked } })}
                  className="w-5 h-5 rounded border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
                />
                <span className="text-sm font-medium text-gray-300">Use SSL/TLS</span>
              </label>
            </div>
          </div>
        </div>

        {/* PostgreSQL Settings */}
        <div>
          <h3 className="text-lg font-medium mb-3 text-white">PostgreSQL Database</h3>
          <div className="space-y-4">
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <div className="md:col-span-2">
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Host
                </label>
                <input
                  type="text"
                  value={data.postgres?.host || ''}
                  onChange={(e) => onChange({ postgres: { ...data.postgres, host: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="e.g., localhost"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Port
                </label>
                <input
                  type="number"
                  value={data.postgres?.port || ''}
                  onChange={(e) => onChange({ postgres: { ...data.postgres, port: safeParseInt(e.target.value, data.postgres?.port || 5432) } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="5432"
                  min="1"
                  max="65535"
                />
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                Database Name
              </label>
              <input
                type="text"
                value={data.postgres?.database || ''}
                onChange={(e) => onChange({ postgres: { ...data.postgres, database: e.target.value } })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                placeholder="e.g., security_camera"
              />
            </div>

            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Username
                </label>
                <input
                  type="text"
                  value={data.postgres?.username || ''}
                  onChange={(e) => onChange({ postgres: { ...data.postgres, username: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Database username"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  Password
                </label>
                <input
                  type="password"
                  value={data.postgres?.password || ''}
                  onChange={(e) => onChange({ postgres: { ...data.postgres, password: e.target.value } })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  placeholder="Enter database password"
                />
                <p className="text-xs text-gray-400 mt-1">
                  {data.postgres?.password ? 'Password is set (shown encrypted)' : 'Required for database authentication'}
                </p>
              </div>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                SSL Mode
              </label>
              <select
                value={data.postgres?.sslMode || 'disable'}
                onChange={(e) => onChange({ postgres: { ...data.postgres, sslMode: e.target.value } })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="disable">Disable</option>
                <option value="require">Require</option>
                <option value="verify-ca">Verify CA</option>
                <option value="verify-full">Verify Full</option>
              </select>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
