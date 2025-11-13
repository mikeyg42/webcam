import type { EmailSettings } from '../../types/api';

interface EmailSectionProps {
  data: EmailSettings;
  onChange: (data: Partial<EmailSettings>) => void;
}

export function EmailSection({ data, onChange }: EmailSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Email Notification Settings</h2>
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Email Method
          </label>
          <select
            value={data.method}
            onChange={(e) => onChange({ method: e.target.value as any })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="disabled">Disabled</option>
            <option value="mailersend">MailerSend</option>
            <option value="gmail">Gmail OAuth2</option>
          </select>
        </div>

        {data.method !== 'disabled' && (
          <>
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  From Email
                </label>
                <input
                  type="email"
                  value={data.fromEmail}
                  onChange={(e) => onChange({ fromEmail: e.target.value })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  To Email
                </label>
                <input
                  type="email"
                  value={data.toEmail}
                  onChange={(e) => onChange({ toEmail: e.target.value })}
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            </div>

            {data.method === 'mailersend' && (
              <div>
                <label className="block text-sm font-medium text-gray-300 mb-2">
                  MailerSend API Token
                </label>
                <input
                  type="password"
                  value={data.mailsendApiToken || ''}
                  onChange={(e) => onChange({ mailsendApiToken: e.target.value })}
                  placeholder="Enter API token (leave blank to keep existing)"
                  className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>
            )}

            {data.method === 'gmail' && (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-gray-300 mb-2">
                    Gmail Client ID
                  </label>
                  <input
                    type="text"
                    value={data.gmailClientId || ''}
                    onChange={(e) => onChange({ gmailClientId: e.target.value })}
                    placeholder="Enter Client ID"
                    className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>

                <div>
                  <label className="block text-sm font-medium text-gray-300 mb-2">
                    Gmail Client Secret
                  </label>
                  <input
                    type="password"
                    value={data.gmailClientSecret || ''}
                    onChange={(e) => onChange({ gmailClientSecret: e.target.value })}
                    placeholder="Enter Client Secret"
                    className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
