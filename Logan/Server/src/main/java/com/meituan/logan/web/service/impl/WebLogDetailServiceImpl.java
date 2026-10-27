package com.meituan.logan.web.service.impl;

import com.meituan.logan.web.dto.WebLogDetailDTO;
import com.meituan.logan.web.mapper.WebLogDetailMapper;
import com.meituan.logan.web.service.WebLogDetailService;
import com.meituan.logan.web.util.OrderUtil;
import org.apache.commons.collections.CollectionUtils;
import org.apache.commons.lang3.StringUtils;
import org.apache.log4j.Logger;
import org.springframework.stereotype.Service;
import org.springframework.transaction.PlatformTransactionManager;
import org.springframework.transaction.support.TransactionTemplate;

import javax.annotation.Resource;
import java.util.Collections;
import java.util.List;
import java.util.stream.Collectors;

/**
 * 功能描述:  <p></p>
 *
 *
 * @version 1.0 2019-10-31
 * @since logan-admin-server 1.0
 */
@Service
public class WebLogDetailServiceImpl extends AbstractBatchInsertService<WebLogDetailDTO> implements WebLogDetailService {
    private static final Logger LOGGER = Logger.getLogger(WebLogDetailServiceImpl.class);

    @Resource
    private WebLogDetailMapper detailMapper;

    @Resource
    private PlatformTransactionManager transactionManager;

    /** The caller immediately queries these details, so return only after commit. */
    @Override
    public void saveLogDetails(List<WebLogDetailDTO> details) {
        if (CollectionUtils.isEmpty(details)) {
            return;
        }
        new TransactionTemplate(transactionManager).execute(status -> {
            for (int start = 0; start < details.size(); start += 200) {
                detailMapper.batchInsert(details.subList(start, Math.min(start + 200, details.size())));
            }
            return null;
        });
    }

    @Override
    public WebLogDetailDTO queryById(long detailId) {
        try {
            return detailMapper.queryById(detailId);
        } catch (Exception e) {
            LOGGER.error(e);
        }
        return null;
    }

    @Override
    public void deleteByRange(long lowerBound, long upperBound) {
        try {
            detailMapper.deleteByRange(lowerBound, upperBound);
        } catch (Exception e) {
            LOGGER.error(e);
        }
    }

    @Override
    public List<WebLogDetailDTO> queryByIds(List<Long> detailIds) {
        try {
            List<WebLogDetailDTO> list = detailMapper.queryByIds(detailIds);
            if (CollectionUtils.isNotEmpty(list)) {
                return OrderUtil.order(list, detailIds, WebLogDetailDTO::getId);
            }
        } catch (Exception e) {
            LOGGER.error(e);
        }
        return Collections.emptyList();
    }

    @Override
    public List<WebLogDetailDTO> match(long taskId, int beginTimeOffset, int endTimeOffset,
            List<Integer> logTypes, String keyword) {
        try {
            List<WebLogDetailDTO> list = detailMapper.match(taskId, beginTimeOffset, endTimeOffset,
                    logTypes);
            if (CollectionUtils.isNotEmpty(list) && StringUtils.isNotEmpty(keyword)) {
                return list.stream().filter(
                        webLogDetailDTO -> webLogDetailDTO.getContent().contains(keyword)).collect(
                        Collectors.toList());
            }
            return list;
        } catch (Exception e) {
            LOGGER.error(e);
        }
        return Collections.emptyList();
    }

    @Override
    protected void execute(List<WebLogDetailDTO> cachedLogDetails) {
        try {
            detailMapper.batchInsert(cachedLogDetails);
        } catch (Exception e) {
            LOGGER.error(e);
        }
    }
}
