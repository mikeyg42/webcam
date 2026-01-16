import { Input } from '../../primitives/Input';
import { Select } from '../../primitives/Select';
import type { EmailSettings } from '../../../types/api';

interface NotificationsAdvancedProps {
  data: EmailSettings;
  onChange: (data: Partial<EmailSettings>) => void;
}

export function NotificationsAdvanced({ data, onChange }: NotificationsAdvancedProps) {
  const methodOptions = [
    { value: 'disabled', label: 'Disabled' },
    { value: 'mailersend', label: 'MailerSend' },
    { value: 'gmail', label: 'Gmail' },
  ];

  return (
    <div className="space-y-4">
      <Select
        label="Notification Method"
        options={methodOptions}
        value={data.method}
        onChange={(e) => onChange({ method: e.target.value as EmailSettings['method'] })}
        helper="How to send motion event notifications"
      />

      {data.method !== 'disabled' && (
        <>
          <div className="grid grid-cols-2 gap-4">
            <Input
              label="From Email"
              type="email"
              value={data.fromEmail}
              onChange={(e) => onChange({ fromEmail: e.target.value })}
              helper="Sender address"
            />
            <Input
              label="To Email"
              type="email"
              value={data.toEmail}
              onChange={(e) => onChange({ toEmail: e.target.value })}
              helper="Recipient address"
            />
          </div>

          {data.method === 'mailersend' && (
            <Input
              label="MailerSend API Token"
              type="password"
              value={data.mailsendApiToken || ''}
              onChange={(e) => onChange({ mailsendApiToken: e.target.value })}
              helper="Your MailerSend API token"
            />
          )}

          {data.method === 'gmail' && (
            <div className="grid grid-cols-2 gap-4">
              <Input
                label="Gmail Client ID"
                value={data.gmailClientId || ''}
                onChange={(e) => onChange({ gmailClientId: e.target.value })}
              />
              <Input
                label="Gmail Client Secret"
                type="password"
                value={data.gmailClientSecret || ''}
                onChange={(e) => onChange({ gmailClientSecret: e.target.value })}
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}

NotificationsAdvanced.displayName = 'NotificationsAdvanced';
