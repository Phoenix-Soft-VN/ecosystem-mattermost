// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';

import type {AppTile} from './client';
import {launcherIcon} from './launcher_icons';

type Props = {
    id: string;
    tenantName: string;
    apps: AppTile[];
    currentApp: string;
    onClose: () => void;
};

export default function AppLauncher({id, tenantName, apps, currentApp, onClose}: Props) {
    const intl = useIntl();
    const [query, setQuery] = useState('');
    const filtered = useMemo(() => {
        const term = query.trim().toLocaleLowerCase();
        return apps.filter((app) => !term || [app.label, app.appCode, app.description].some((text) => text?.toLocaleLowerCase().includes(term)));
    }, [apps, query]);

    return (
        <section
            id={id}
            className='phoenix-shell__popover phoenix-shell__launcher'
            role='dialog'
            aria-modal='false'
            aria-labelledby={`${id}-title`}
            data-phoenix-popover='apps'
            tabIndex={-1}
        >
            <div className='phoenix-shell__launcher-heading'>
                <div className='phoenix-shell__launcher-scope'>
                    <h2 id={`${id}-title`}>
                        <FormattedMessage
                            id='phoenix.launcher.title'
                            defaultMessage='Ứng dụng của bạn'
                        />
                    </h2>
                    <span title={tenantName}>{tenantName}</span>
                </div>
                <button
                    type='button'
                    className='phoenix-shell__icon-button'
                    aria-label={intl.formatMessage({id: 'phoenix.launcher.close', defaultMessage: 'Đóng'})}
                    onClick={onClose}
                >
                    <span aria-hidden='true'>{'×'}</span>
                </button>
            </div>
            <input
                className='phoenix-shell__launcher-search'
                type='search'
                aria-label={intl.formatMessage({id: 'phoenix.launcher.search', defaultMessage: 'Tìm ứng dụng'})}
                placeholder={intl.formatMessage({id: 'phoenix.launcher.search', defaultMessage: 'Tìm ứng dụng'})}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
            />
            {filtered.length ? (
                <ul className='phoenix-shell__launcher-grid'>
                    {filtered.map((app) => {
                        const current = app.appCode === currentApp;
                        const launchable = !current && app.state === 'ready' && Boolean(app.launchUrl);
                        let hint: React.ReactNode = app.description;
                        if (current) {
                            hint = (
                                <FormattedMessage
                                    id='phoenix.launcher.current'
                                    defaultMessage='Hiện tại'
                                />
                            );
                        } else if (app.state === 'provisioning') {
                            hint = (
                                <FormattedMessage
                                    id='phoenix.launcher.provisioning'
                                    defaultMessage='Đang chuẩn bị'
                                />
                            );
                        } else if (launchable) {
                            hint = app.description;
                        } else {
                            hint = (
                                <FormattedMessage
                                    id='phoenix.launcher.disabled'
                                    defaultMessage='Chưa sẵn sàng'
                                />
                            );
                        }
                        const content = (
                            <>
                                <span className='phoenix-shell__launcher-icon'>{launcherIcon(app.iconKey || app.appCode)}</span>
                                <span className='phoenix-shell__launcher-name'>{app.label}</span>
                                <span className='phoenix-shell__launcher-description'>
                                    {hint}
                                </span>
                                {current && (
                                    <span
                                        className='phoenix-shell__launcher-current'
                                        aria-hidden='true'
                                    />
                                )}
                            </>
                        );
                        return (
                            <li key={app.appCode}>
                                {launchable ? (
                                    <a
                                        className='phoenix-shell__launcher-tile'
                                        href={app.launchUrl}
                                        aria-label={app.label}
                                        title={app.label}
                                        rel='noopener noreferrer'
                                    >
                                        {content}
                                    </a>
                                ) : (
                                    <span
                                        className='phoenix-shell__launcher-tile phoenix-shell__launcher-tile--disabled'
                                        aria-current={current ? 'page' : undefined}
                                        aria-disabled={!current || undefined}
                                        title={app.label}
                                    >
                                        {content}
                                    </span>
                                )}
                            </li>
                        );
                    })}
                </ul>
            ) : (
                <p
                    className='phoenix-shell__menu-empty'
                    role='status'
                >
                    {apps.length ? (
                        <FormattedMessage
                            id='phoenix.launcher.noMatches'
                            defaultMessage='Không tìm thấy ứng dụng.'
                        />
                    ) : (
                        <FormattedMessage
                            id='phoenix.launcher.empty'
                            defaultMessage='Bạn chưa được cấp ứng dụng'
                        />
                    )}
                </p>
            )}
            <div className='phoenix-shell__launcher-footer'>
                <FormattedMessage
                    id='phoenix.launcher.session'
                    defaultMessage='Cùng phiên đăng nhập'
                />
            </div>
        </section>
    );
}
