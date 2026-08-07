package com.meituan.logan.web.task;

import com.meituan.logan.web.mapper.LoganLogDetailMapper;
import com.meituan.logan.web.mapper.LoganTaskMapper;
import com.meituan.logan.web.service.WebLogDetailService;
import com.meituan.logan.web.service.WebTaskService;
import com.meituan.logan.web.util.OssUtil;
import org.apache.commons.collections.CollectionUtils;
import org.apache.log4j.Logger;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.stereotype.Component;

import javax.annotation.Resource;
import java.util.List;

/**
 * 日志定时清理任务
 * <p>
 * 每天凌晨 2 点执行，清理超过保留天数（默认 7 天）的日志数据：
 * 1. 原生端：删除过期 logan_task + logan_log_detail 记录 + OSS 文件
 * 2. Web 端：删除过期 web_task + web_detail 记录
 * </p>
 *
 * @since 2026-08-06
 */
@Component
public class LogCleanTask {

    private static final Logger LOGGER = Logger.getLogger(LogCleanTask.class);

    @Resource
    private LoganTaskMapper loganTaskMapper;
    @Resource
    private LoganLogDetailMapper loganLogDetailMapper;
    @Resource
    private WebTaskService webTaskService;
    @Resource
    private WebLogDetailService webLogDetailService;

    /**
     * 每天凌晨 2 点执行清理
     */
    @Scheduled(cron = "0 0 2 * * ?")
    public void cleanExpiredLogs() {
        LOGGER.info("Starting log cleanup task...");
        long startTime = System.currentTimeMillis();

        int expireDays = OssUtil.getExpireDays();
        long cutoff = System.currentTimeMillis() - expireDays * 24 * 60 * 60 * 1000L;

        try {
            cleanNativeLogs(cutoff);
            cleanWebLogs(cutoff);
        } catch (Exception e) {
            LOGGER.error("Log cleanup task failed", e);
        }

        long duration = System.currentTimeMillis() - startTime;
        LOGGER.info("Log cleanup task completed in " + duration + "ms");
    }

    /**
     * 清理原生端过期日志：
     * 1. 查询过期任务 ID 列表
     * 2. 删除对应的 log_detail 记录
     * 3. 删除 log_task 记录
     * 4. 删除 OSS 文件（可选，OSS 生命周期规则会自动处理）
     */
    private void cleanNativeLogs(long cutoff) {
        List<Long> expiredTaskIds = loganTaskMapper.selectIdsByAddTime(cutoff);
        if (CollectionUtils.isEmpty(expiredTaskIds)) {
            return;
        }

        // 分批删除详情记录，避免 IN 子句过长
        int batchSize = 500;
        for (int i = 0; i < expiredTaskIds.size(); i += batchSize) {
            int end = Math.min(i + batchSize, expiredTaskIds.size());
            List<Long> batch = expiredTaskIds.subList(i, end);
            try {
                loganLogDetailMapper.deleteByTaskIds(batch);
            } catch (Exception e) {
                LOGGER.error("Failed to delete log_detail batch, size=" + batch.size(), e);
            }
        }

        // 删除过期任务记录
        try {
            loganTaskMapper.deleteByAddTime(cutoff);
        } catch (Exception e) {
            LOGGER.error("Failed to delete expired logan_task records", e);
        }

        LOGGER.info("Cleaned " + expiredTaskIds.size() + " native log tasks");
    }

    /**
     * 清理 Web 端过期日志：
     * 1. 删除过期 web_task 记录（已有接口）
     * 2. Web 端详情在 web_task 删除后通过关联查询清理
     */
    private void cleanWebLogs(long cutoff) {
        try {
            webTaskService.deleteByAddTime(cutoff);
            LOGGER.info("Cleaned expired web_task records");
        } catch (Exception e) {
            LOGGER.error("Failed to clean web_task records", e);
        }
    }
}
